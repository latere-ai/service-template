package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// declaredSettings is the declaration this repository ships, used so the tests
// exercise the real values rather than a reduced copy.
func declaredSettings(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "settings.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// fakeAPI is a repository API with a live configuration a test controls. It
// records every mutating request, which is how a test proves that a plan and a
// report changed nothing.
type fakeAPI struct {
	mu         sync.Mutex
	repository map[string]any
	protection map[string]any
	alerts     bool
	Writes     []string
	Server     *httptest.Server
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{
		repository: map[string]any{
			"default_branch":              "main",
			"allow_squash_merge":          true,
			"allow_merge_commit":          false,
			"allow_rebase_merge":          false,
			"squash_merge_commit_title":   "PR_TITLE",
			"squash_merge_commit_message": "PR_BODY",
			"delete_branch_on_merge":      true,
			"allow_auto_merge":            true,
			"allow_update_branch":         true,
			"security_and_analysis": map[string]any{
				"secret_scanning":                 map[string]any{"status": "enabled"},
				"secret_scanning_push_protection": map[string]any{"status": "enabled"},
			},
		},
		protection: map[string]any{
			"required_status_checks": map[string]any{
				"strict":   true,
				"contexts": []string{"verify / gate"},
			},
			"required_pull_request_reviews": map[string]any{
				"dismiss_stale_reviews":           true,
				"require_code_owner_reviews":      true,
				"required_approving_review_count": 1,
			},
			"required_linear_history":          map[string]any{"enabled": true},
			"required_conversation_resolution": map[string]any{"enabled": true},
			"allow_force_pushes":               map[string]any{"enabled": false},
			"allow_deletions":                  map[string]any{"enabled": false},
			"enforce_admins":                   map[string]any{"enabled": false},
		},
		alerts: true,
	}
	api.Server = httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.Server.Close)
	return api
}

func (a *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if r.Method != http.MethodGet {
		a.Writes = append(a.Writes, r.Method+" "+r.URL.Path)
	}

	switch {
	case r.URL.Path == "/repos/acme/pay-api" && r.Method == http.MethodGet:
		writeJSON(w, a.repository)
	case r.URL.Path == "/repos/acme/pay-api" && r.Method == http.MethodPatch:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		maps.Copy(a.repository, body)
		writeJSON(w, a.repository)
	case r.URL.Path == "/repos/acme/pay-api/vulnerability-alerts":
		switch r.Method {
		case http.MethodGet:
			if a.alerts {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			a.alerts = true
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			a.alerts = false
			w.WriteHeader(http.StatusNoContent)
		}
	case strings.HasSuffix(r.URL.Path, "/protection"):
		switch r.Method {
		case http.MethodGet:
			if a.protection == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, a.protection)
		case http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			a.protection = normalizeProtection(body)
			writeJSON(w, a.protection)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// normalizeProtection turns the request body into the shape the read endpoint
// answers with, so a repeat run sees what it wrote.
func normalizeProtection(body map[string]any) map[string]any {
	enabled := func(key string) map[string]any {
		value, _ := body[key].(bool)
		return map[string]any{"enabled": value}
	}
	return map[string]any{
		"required_status_checks":           body["required_status_checks"],
		"required_pull_request_reviews":    body["required_pull_request_reviews"],
		"required_linear_history":          enabled("required_linear_history"),
		"required_conversation_resolution": enabled("required_conversation_resolution"),
		"allow_force_pushes":               enabled("allow_force_pushes"),
		"allow_deletions":                  enabled("allow_deletions"),
		"enforce_admins":                   enabled("enforce_admins"),
	}
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (a *fakeAPI) writes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.Writes...)
}

type outcome struct {
	Code   int
	Stdout string
	Stderr string
}

func runSettings(t *testing.T, args ...string) outcome {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run(args, &stdout, &stderr)
	return outcome{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

func settingsArgs(t *testing.T, api *fakeAPI, mode string) []string {
	t.Helper()
	return []string{
		"-mode", mode,
		"-file", writeTemp(t, "settings.yml", declaredSettings(t)),
		"-repo", "acme/pay-api",
		"-api-url", api.Server.URL,
		"-token", "test-token",
	}
}

// A dry run prints the difference and changes nothing. Without this property
// nobody runs the tool against a live repository to find out what it would do.
func TestPlanChangesNothing(t *testing.T) {
	api := newFakeAPI(t)
	api.repository["allow_merge_commit"] = true
	api.repository["delete_branch_on_merge"] = false

	got := runSettings(t, settingsArgs(t, api, "plan")...)
	if got.Code != exitOK {
		t.Fatalf("plan exited %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	}
	for _, want := range []string{"allow_merge_commit", "delete_branch_on_merge", "a plan changes nothing"} {
		if !strings.Contains(got.Stdout, want) {
			t.Fatalf("the plan does not mention %q\n%s", want, got.Stdout)
		}
	}
	if writes := api.writes(); len(writes) != 0 {
		t.Fatalf("a plan issued %d writes: %v", len(writes), writes)
	}
}

// A drifted repository can fail the run when the caller asks for it, which is
// how a check job reports configuration drift.
func TestPlanCanFailOnDrift(t *testing.T) {
	api := newFakeAPI(t)
	api.repository["allow_rebase_merge"] = true
	got := runSettings(t, append(settingsArgs(t, api, "plan"), "-fail-on-drift")...)
	if got.Code != exitDrift {
		t.Fatalf("plan exited %d, want %d\n%s", got.Code, exitDrift, got.Stdout)
	}
}

// Applying twice produces no second change. An apply that writes every time
// cannot be scheduled, because every run would look like a change.
func TestApplyIsIdempotent(t *testing.T) {
	api := newFakeAPI(t)
	api.repository["allow_merge_commit"] = true
	api.protection = nil
	api.alerts = false

	first := runSettings(t, settingsArgs(t, api, "apply")...)
	if first.Code != exitOK {
		t.Fatalf("the first apply exited %d\n%s%s", first.Code, first.Stdout, first.Stderr)
	}
	firstWrites := len(api.writes())
	if firstWrites == 0 {
		t.Fatal("the first apply wrote nothing to a repository that differed")
	}

	second := runSettings(t, settingsArgs(t, api, "apply")...)
	if second.Code != exitOK {
		t.Fatalf("the second apply exited %d\n%s%s", second.Code, second.Stdout, second.Stderr)
	}
	if got := len(api.writes()); got != firstWrites {
		t.Fatalf("the second apply issued %d more writes: %v", got-firstWrites, api.writes()[firstWrites:])
	}
	if !strings.Contains(second.Stdout, "nothing to apply") {
		t.Fatalf("the second apply did not report a match\n%s", second.Stdout)
	}
}

// An unprotected branch is a distinct report from a rule with the wrong
// values, and applying it creates the rule.
func TestApplyProtectsAnUnprotectedBranch(t *testing.T) {
	api := newFakeAPI(t)
	api.protection = nil

	plan := runSettings(t, settingsArgs(t, api, "plan")...)
	if !strings.Contains(plan.Stdout, "unprotected") {
		t.Fatalf("the plan does not report the unprotected branch\n%s", plan.Stdout)
	}

	required := runSettings(t, settingsArgs(t, api, "required-check")...)
	if required.Code != exitDrift {
		t.Fatalf("required-check exited %d on an unprotected branch, want %d", required.Code, exitDrift)
	}
	if !strings.Contains(required.Stderr, "verify / gate") {
		t.Fatalf("required-check does not name the missing context\n%s", required.Stderr)
	}

	if got := runSettings(t, settingsArgs(t, api, "apply")...); got.Code != exitOK {
		t.Fatalf("apply exited %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	}
	if got := runSettings(t, settingsArgs(t, api, "required-check")...); got.Code != exitOK {
		t.Fatalf("required-check still fails after an apply\n%s%s", got.Stdout, got.Stderr)
	}
}

// The scheduled run reports drift and does not revert it, because a setting
// changed deliberately during an incident must not be undone without notice.
func TestReportDoesNotRevert(t *testing.T) {
	api := newFakeAPI(t)
	api.repository["allow_merge_commit"] = true
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)

	got := runSettings(t, settingsArgs(t, api, "report")...)
	if got.Code != exitOK {
		t.Fatalf("the report exited %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	}
	if writes := api.writes(); len(writes) != 0 {
		t.Fatalf("the report reverted drift with %d writes: %v", len(writes), writes)
	}
	if !strings.Contains(got.Stdout, "::warning title=Repository settings drift::") {
		t.Fatalf("the report raised no annotation\n%s", got.Stdout)
	}
	if live, _ := api.repository["allow_merge_commit"].(bool); !live {
		t.Fatal("the report changed the live configuration")
	}

	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	for _, want := range []string{"allow_merge_commit", "not reverted"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the summary does not carry %q\n%s", want, data)
		}
	}
}

// A missing required check is the state where every gate in the pipeline is
// advisory, so it is reported on its own rather than as one row among many.
func TestRequiredCheckMissingContext(t *testing.T) {
	api := newFakeAPI(t)
	api.protection["required_status_checks"] = map[string]any{
		"strict":   true,
		"contexts": []string{"other / lint"},
	}
	got := runSettings(t, settingsArgs(t, api, "required-check")...)
	if got.Code != exitDrift {
		t.Fatalf("required-check exited %d, want %d\n%s%s", got.Code, exitDrift, got.Stdout, got.Stderr)
	}
	if !strings.Contains(got.Stderr, "advisory") {
		t.Fatalf("required-check does not say what a missing gate means\n%s", got.Stderr)
	}
}

// A dry run must be unable to write, not merely uninclined to.
func TestDryRunClientRefusesToWrite(t *testing.T) {
	client := NewClient("https://api.invalid", "token", "acme/pay-api")
	client.DryRun = true
	err := client.Send(t.Context(), http.MethodPut, "/repos/acme/pay-api", nil)
	if err == nil {
		t.Fatal("a dry run client performed a write")
	}
	if !strings.Contains(err.Error(), "never writes") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}
