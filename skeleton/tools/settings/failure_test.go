package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every way the command can be called wrong reports what is missing, because
// the alternative is a run that fails halfway through with a partial apply.
func TestCommandLineFailures(t *testing.T) {
	api := newFakeAPI(t)
	settings := writeTemp(t, "settings.yml", declaredSettings(t))

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "an unknown mode",
			args: []string{"-mode", "enforce", "-file", settings, "-repo", "acme/pay-api",
				"-api-url", api.Server.URL, "-token", "test-token"},
			want: "unknown mode",
		},
		{
			name: "no repository",
			args: []string{"-mode", "plan", "-file", settings, "-repo", "", "-token", "test-token"},
			want: "-repo is required",
		},
		{
			name: "no token",
			args: []string{"-mode", "plan", "-file", settings, "-repo", "acme/pay-api", "-token", ""},
			want: "GITHUB_TOKEN",
		},
		{
			name: "no declaration",
			args: []string{"-mode", "verify", "-file", filepath.Join(t.TempDir(), "absent.yml")},
			want: "read",
		},
		{
			name: "no ownership file",
			args: []string{"-mode", "verify", "-file", settings,
				"-codeowners", filepath.Join(t.TempDir(), "absent")},
			want: "read",
		},
		{
			name: "an unknown flag",
			args: []string{"-nope"},
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runSettings(t, c.args...)
			if got.Code != exitError {
				t.Fatalf("exited %d, want %d\n%s%s", got.Code, exitError, got.Stdout, got.Stderr)
			}
			if c.want != "" && !strings.Contains(got.Stderr, c.want) {
				t.Fatalf("the failure does not mention %q\n%s", c.want, got.Stderr)
			}
		})
	}
}

// Protection cannot require a check that has never reported, and that answer
// arrives as a 422 with no explanation of what to do about it.
func TestBootstrapOrderingIsExplained(t *testing.T) {
	api := newFakeAPI(t)
	api.protection = nil
	api.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/protection") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":["verify / gate not found"]}`))
			return
		}
		api.serve(w, r)
	})

	got := runSettings(t, settingsArgs(t, api, "apply")...)
	if got.Code != exitError {
		t.Fatalf("apply exited %d on a rejected protection rule", got.Code)
	}
	if !strings.Contains(got.Stderr, "reported at least once") {
		t.Fatalf("the failure does not explain the bootstrap order\n%s", got.Stderr)
	}
}

// An API that answers with an error must not read as a clean repository.
func TestReadFailureIsNotACleanRepository(t *testing.T) {
	api := newFakeAPI(t)
	api.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"Server Error"}`))
	})
	got := runSettings(t, settingsArgs(t, api, "plan")...)
	if got.Code != exitError {
		t.Fatalf("plan exited %d on a failing API", got.Code)
	}
	if !strings.Contains(got.Stderr, "Server Error") {
		t.Fatalf("the failure does not carry the API message\n%s", got.Stderr)
	}
}

// The security settings live behind three different endpoints, so each one is
// applied and then reads back as applied.
func TestApplySecuritySettings(t *testing.T) {
	api := newFakeAPI(t)
	api.alerts = false
	api.repository["security_and_analysis"] = map[string]any{
		"secret_scanning":                 map[string]any{"status": "disabled"},
		"secret_scanning_push_protection": map[string]any{"status": "disabled"},
	}

	plan := runSettings(t, settingsArgs(t, api, "plan")...)
	for _, want := range []string{"vulnerability_alerts", "secret_scanning", "secret_scanning_push_protection"} {
		if !strings.Contains(plan.Stdout, want) {
			t.Fatalf("the plan does not report %q\n%s", want, plan.Stdout)
		}
	}

	if got := runSettings(t, settingsArgs(t, api, "apply")...); got.Code != exitOK {
		t.Fatalf("apply exited %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	}
	if !api.alerts {
		t.Error("vulnerability alerts are still off after an apply")
	}
	after := runSettings(t, settingsArgs(t, api, "plan")...)
	if !strings.Contains(after.Stdout, "matches the declaration") {
		t.Fatalf("the repository still differs after an apply\n%s", after.Stdout)
	}
}

// A repository with no security block at all reads as disabled rather than as
// absent, because an absent state cannot be compared and would report clean.
func TestMissingSecurityBlockReadsAsDisabled(t *testing.T) {
	api := newFakeAPI(t)
	delete(api.repository, "security_and_analysis")
	got := runSettings(t, settingsArgs(t, api, "plan")...)
	if !strings.Contains(got.Stdout, "secret_scanning") {
		t.Fatalf("a repository with no security block reads as configured\n%s", got.Stdout)
	}
}

// A report on a repository that matches says so, and writes that into the run
// summary, so a scheduled run that found nothing is still evidence.
func TestReportWithoutDrift(t *testing.T) {
	api := newFakeAPI(t)
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)

	got := runSettings(t, settingsArgs(t, api, "report")...)
	if got.Code != exitOK {
		t.Fatalf("the report exited %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	}
	if !strings.Contains(got.Stdout, "matches the declaration") {
		t.Fatalf("the report does not state the match\n%s", got.Stdout)
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	if !strings.Contains(string(data), "matches the declaration") {
		t.Fatalf("the summary does not state the match\n%s", data)
	}
}

// The endpoint is read from the environment when the flag is absent, which is
// how the tool reaches an enterprise host.
func TestEnvironmentDefaults(t *testing.T) {
	if got := envOr("SETTINGS_TEST_ABSENT", "fallback"); got != "fallback" {
		t.Errorf("envOr returned %q, want the fallback", got)
	}
	t.Setenv("SETTINGS_TEST_PRESENT", "value")
	if got := envOr("SETTINGS_TEST_PRESENT", "fallback"); got != "value" {
		t.Errorf("envOr returned %q, want the environment value", got)
	}
}
