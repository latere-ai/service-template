package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the small part of the repository API this tool needs.
//
// It records every write it issues. A dry run refuses to write at all, which
// is the property the tests assert: a plan that can write is not a plan.
type Client struct {
	BaseURL string
	Token   string
	Repo    string
	HTTP    *http.Client
	DryRun  bool

	// Writes holds "METHOD path" for every mutating request, in order.
	Writes []string
}

// NewClient builds a client for one repository.
func NewClient(baseURL, token, repo string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Token:   token,
		Repo:    repo,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode the request body: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, payload)
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

// Get reads a resource. The status code is returned because an absent
// resource is a state this tool reads rather than an error: a branch with no
// protection answers 404, and that is the drift to report.
func (c *Client) Get(ctx context.Context, path string, out any) (int, error) {
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, apiError(http.MethodGet, path, resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode GET %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// Send performs a mutating request. In a dry run it refuses, so a plan cannot
// change the repository even by mistake.
func (c *Client) Send(ctx context.Context, method, path string, body any) error {
	if c.DryRun {
		return fmt.Errorf("a dry run attempted %s %s; a plan reports and never writes", method, path)
	}
	c.Writes = append(c.Writes, method+" "+path)
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return apiError(method, path, resp)
	}
	return nil
}

func apiError(method, path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	// Protection cannot require a check that has never reported, which is the
	// one failure a new repository hits and the one that reads as a bug.
	if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(path, "/protection") {
		return fmt.Errorf("%s %s: %s\n"+
			"a required status check must have reported at least once before protection can require it. "+
			"Merge the first pull request with the pipeline in place, then apply the settings", method, path, message)
	}
	return fmt.Errorf("%s %s: %s", method, path, message)
}

// repoPath builds a path under the repository.
func (c *Client) repoPath(suffix string) string {
	return "/repos/" + c.Repo + suffix
}
