package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// Probe paths the runtime contract fixes. They are the same three strings an
// orchestrator manifest and a dashboard hold.
const (
	readyPath   = "/readyz"
	versionPath = "/version"
	shellPath   = "/"
)

// maxBody bounds how much of a response the run reads. An assertion needs the
// first part of a document, and an unbounded read turns a misrouted request
// into a memory problem.
const maxBody = 512 << 10

// bodyExcerpt is how much of a body the evidence block prints.
const bodyExcerpt = 160

// readyBody is the readiness response the runtime contract defines.
type readyBody struct {
	Status string `json:"status"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"checks"`
}

// versionBody is the build identity the runtime contract defines.
type versionBody struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	AssetHash string `json:"asset_hash"`
}

// response is one fetched HTTP response, already read.
type response struct {
	status int
	body   []byte
}

// fetch performs one request against the live target.
func fetch(ctx context.Context, client *http.Client, method, base, p string, header map[string]string) (response, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+p, nil)
	if err != nil {
		return response{}, fmt.Errorf("build request for %s: %w", p, err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("request %s %s: %w", method, p, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return response{}, fmt.Errorf("read body of %s %s: %w", method, p, err)
	}
	return response{status: resp.StatusCode, body: body}, nil
}

// Assertions builds the list the run executes: readiness, the three build
// identity fields, the entry asset the live document references, and the
// consumer's own checks.
func Assertions(cfg Config, client *http.Client) ([]Assertion, error) {
	list := []Assertion{readiness(cfg, client)}
	list = append(list, buildIdentity(cfg, client)...)
	if cfg.HasFrontend() {
		list = append(list, servedBundle(cfg, client))
	}

	checks, err := LoadChecks(cfg.ChecksPath)
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		list = append(list, consumerCheck(cfg, client, c))
	}
	return list, nil
}

// readiness asserts the service reports every registered dependency healthy.
func readiness(cfg Config, client *http.Client) Assertion {
	return Assertion{
		Name:     "readiness",
		Expected: "200 with every dependency ok",
		Check: func(ctx context.Context) (string, error) {
			resp, err := fetch(ctx, client, http.MethodGet, cfg.BaseURL, readyPath, nil)
			if err != nil {
				return "no response", err
			}
			var body readyBody
			if err := json.Unmarshal(resp.body, &body); err != nil {
				return fmt.Sprintf("%d %s", resp.status, excerpt(resp.body)),
					fmt.Errorf("parse %s body: %w", readyPath, err)
			}
			states := make([]string, 0, len(body.Checks))
			var failing []string
			for _, c := range body.Checks {
				state := c.Name + "=" + c.Status
				if c.Error != "" {
					state += " (" + c.Error + ")"
				}
				states = append(states, state)
				if c.Status != "ok" {
					failing = append(failing, c.Name)
				}
			}
			observed := fmt.Sprintf("%d status=%s checks=[%s]", resp.status, body.Status, strings.Join(states, ", "))
			switch {
			case resp.status != http.StatusOK:
				return observed, fmt.Errorf("%s returned %d", readyPath, resp.status)
			case len(failing) > 0:
				return observed, fmt.Errorf("dependencies not ready: %s", strings.Join(failing, ", "))
			}
			return observed, nil
		},
	}
}

// buildIdentity asserts the live service reports the build the release made.
// The three fields are separate assertions because each one fails for its own
// reason: a stale replica, a tag on the wrong commit, and a binary carrying
// the previous bundle are three different faults.
func buildIdentity(cfg Config, client *http.Client) []Assertion {
	read := func(ctx context.Context) (versionBody, string, error) {
		resp, err := fetch(ctx, client, http.MethodGet, cfg.BaseURL, versionPath, nil)
		if err != nil {
			return versionBody{}, "no response", err
		}
		if resp.status != http.StatusOK {
			return versionBody{}, fmt.Sprintf("%d %s", resp.status, excerpt(resp.body)),
				fmt.Errorf("%s returned %d", versionPath, resp.status)
		}
		var body versionBody
		if err := json.Unmarshal(resp.body, &body); err != nil {
			return versionBody{}, excerpt(resp.body), fmt.Errorf("parse %s body: %w", versionPath, err)
		}
		return body, "", nil
	}

	field := func(name, expected string, pick func(versionBody) string) Assertion {
		return Assertion{
			Name:     "build identity: " + name,
			Expected: expected,
			Check: func(ctx context.Context) (string, error) {
				body, observed, err := read(ctx)
				if err != nil {
					return observed, err
				}
				got := pick(body)
				if got != expected {
					return got, fmt.Errorf("live %s is %q, the release built %q", name, got, expected)
				}
				return got, nil
			},
		}
	}

	list := []Assertion{
		field("version", cfg.ExpectVersion, func(b versionBody) string { return b.Version }),
		field("commit", cfg.ExpectCommit, func(b versionBody) string { return b.Commit }),
	}
	if cfg.HasFrontend() {
		list = append(list, field("entry asset", cfg.ExpectAsset, func(b versionBody) string { return b.AssetHash }))
	}
	return list
}

// assetRef matches a script or stylesheet reference in the served document.
var assetRef = regexp.MustCompile(`(?:src|href)=["']([^"']+)["']`)

// servedBundle asserts the document the target serves references the entry
// asset the release embedded. A binary can report the right asset hash and
// still serve a cached document from a previous release, and that state looks
// like a successful deploy everywhere else.
func servedBundle(cfg Config, client *http.Client) Assertion {
	return Assertion{
		Name:     "served bundle",
		Expected: "document references " + cfg.ExpectAsset,
		Check: func(ctx context.Context) (string, error) {
			resp, err := fetch(ctx, client, http.MethodGet, cfg.BaseURL, shellPath, map[string]string{
				"Accept": "text/html",
			})
			if err != nil {
				return "no response", err
			}
			if resp.status != http.StatusOK {
				return fmt.Sprintf("%d %s", resp.status, excerpt(resp.body)),
					fmt.Errorf("%s returned %d", shellPath, resp.status)
			}
			var refs []string
			for _, m := range assetRef.FindAllStringSubmatch(string(resp.body), -1) {
				refs = append(refs, path.Base(m[1]))
			}
			observed := strings.Join(refs, ", ")
			if observed == "" {
				observed = "no asset references"
			}
			for _, ref := range refs {
				if ref == cfg.ExpectAsset {
					return ref, nil
				}
			}
			return observed, fmt.Errorf("the served document does not reference %s", cfg.ExpectAsset)
		},
	}
}

// consumerCheck runs one assertion the consumer declared.
func consumerCheck(cfg Config, client *http.Client, c Check) Assertion {
	expected := fmt.Sprintf("%s %s -> %d", c.Method, c.Path, c.Status)
	if c.Contains != "" {
		expected += fmt.Sprintf(" containing %q", c.Contains)
	}
	return Assertion{
		Name:     c.Name,
		Expected: expected,
		Check: func(ctx context.Context) (string, error) {
			resp, err := fetch(ctx, client, c.Method, cfg.BaseURL, c.Path, c.Header)
			if err != nil {
				return "no response", err
			}
			observed := fmt.Sprintf("%d %s", resp.status, excerpt(resp.body))
			if resp.status != c.Status {
				return observed, fmt.Errorf("%s %s returned %d, want %d", c.Method, c.Path, resp.status, c.Status)
			}
			if c.Contains != "" && !strings.Contains(string(resp.body), c.Contains) {
				return observed, fmt.Errorf("%s %s body does not contain %q", c.Method, c.Path, c.Contains)
			}
			return observed, nil
		},
	}
}

// excerpt renders the readable head of a body on one line, so the evidence
// table stays a table.
func excerpt(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) > bodyExcerpt {
		return s[:bodyExcerpt] + "..."
	}
	return s
}
