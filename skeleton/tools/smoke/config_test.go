package main

import (
	"strings"
	"testing"
	"time"
)

// env returns a getenv function over a map, so a test drives the smoke run
// without mutating the process environment.
func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// validEnv is the smallest environment a run accepts.
func validEnv() map[string]string {
	return map[string]string{
		EnvBaseURL:       "https://service.example/",
		EnvExpectVersion: "v1.2.3",
		EnvExpectCommit:  "abc123",
		EnvExpectAsset:   "index-C3xK9pQ2.js",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(env(validEnv()))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseURL != "https://service.example" {
		t.Errorf("BaseURL = %q, want the trailing slash removed", cfg.BaseURL)
	}
	if cfg.Window != DefaultWindow || cfg.Backoff != DefaultBackoff || cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("durations = %s/%s/%s, want the documented defaults",
			cfg.Window, cfg.Backoff, cfg.RequestTimeout)
	}
	if cfg.Target != "unnamed" {
		t.Errorf("Target = %q, want the placeholder", cfg.Target)
	}
	if !cfg.HasFrontend() {
		t.Error("HasFrontend = false for an asset expectation")
	}
}

func TestLoadConfigMissingRequired(t *testing.T) {
	for _, missing := range []string{EnvBaseURL, EnvExpectVersion, EnvExpectCommit, EnvExpectAsset} {
		t.Run(missing, func(t *testing.T) {
			values := validEnv()
			delete(values, missing)
			_, err := LoadConfig(env(values))
			if err == nil {
				t.Fatalf("LoadConfig accepted an environment with no %s", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error %q does not name %s", err, missing)
			}
		})
	}
}

// An empty entry-asset expectation would make the check that catches a stale
// bundle pass unconditionally, so a service with no frontend must say so.
func TestLoadConfigNoFrontendIsExplicit(t *testing.T) {
	values := validEnv()
	values[EnvExpectAsset] = NoFrontend
	cfg, err := LoadConfig(env(values))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.HasFrontend() {
		t.Error("HasFrontend = true for the no-frontend expectation")
	}
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	cases := map[string]struct{ key, value string }{
		"relative url":     {EnvBaseURL, "service.example"},
		"bad duration":     {EnvWindow, "soon"},
		"zero duration":    {EnvBackoff, "0s"},
		"negative timeout": {EnvRequestTimeout, "-1s"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			values := validEnv()
			values[c.key] = c.value
			if _, err := LoadConfig(env(values)); err == nil {
				t.Fatalf("LoadConfig accepted %s=%q", c.key, c.value)
			}
		})
	}
}

func TestLoadConfigEvidenceFallsBackToStepSummary(t *testing.T) {
	values := validEnv()
	values[EnvStepSummary] = "/tmp/summary.md"
	cfg, err := LoadConfig(env(values))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EvidencePath != "/tmp/summary.md" {
		t.Errorf("EvidencePath = %q, want the run summary", cfg.EvidencePath)
	}

	values[EnvEvidence] = "/tmp/explicit.md"
	cfg, err = LoadConfig(env(values))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EvidencePath != "/tmp/explicit.md" {
		t.Errorf("EvidencePath = %q, want the explicit file to win", cfg.EvidencePath)
	}
}

func TestLoadConfigOverridesDurations(t *testing.T) {
	values := validEnv()
	values[EnvWindow] = "30s"
	cfg, err := LoadConfig(env(values))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Window != 30*time.Second {
		t.Errorf("Window = %s, want 30s", cfg.Window)
	}
}
