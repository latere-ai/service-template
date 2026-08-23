package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts content at a path inside a temporary directory and returns it.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadChecksAppliesDefaults(t *testing.T) {
	path := write(t, t.TempDir(), "checks.yaml", `
checks:
  - name: list widgets
    path: /v1/widgets
  - name: create widget
    path: /v1/widgets
    method: POST
    status: 201
    contains: '"id"'
`)
	checks, err := LoadChecks(path)
	if err != nil {
		t.Fatalf("LoadChecks: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(checks))
	}
	if checks[0].Method != "GET" || checks[0].Status != 200 {
		t.Errorf("defaults not applied: %+v", checks[0])
	}
	if checks[1].Method != "POST" || checks[1].Status != 201 {
		t.Errorf("explicit values overwritten: %+v", checks[1])
	}
}

// A named file that is absent is a typo in the pipeline, and a typo must not
// quietly reduce the run to the template's own assertions.
func TestLoadChecksExplicitMissingIsAnError(t *testing.T) {
	if _, err := LoadChecks(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("LoadChecks accepted a named file that does not exist")
	}
}

func TestLoadChecksDefaultPathMayBeAbsent(t *testing.T) {
	t.Chdir(t.TempDir())
	checks, err := LoadChecks("")
	if err != nil {
		t.Fatalf("LoadChecks: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("got %d checks from an empty tree", len(checks))
	}
}

func TestLoadChecksReadsTheDefaultPath(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, DefaultChecksPath, "checks:\n  - name: liveness\n    path: /livez\n")
	t.Chdir(dir)
	checks, err := LoadChecks("")
	if err != nil {
		t.Fatalf("LoadChecks: %v", err)
	}
	if len(checks) != 1 || checks[0].Name != "liveness" {
		t.Fatalf("got %+v, want the default file's single check", checks)
	}
}

func TestLoadChecksRejectsMalformedEntries(t *testing.T) {
	cases := map[string]string{
		"no name":       "checks:\n  - path: /v1/widgets\n",
		"relative path": "checks:\n  - name: widgets\n    path: v1/widgets\n",
		"not yaml":      "checks: [\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := write(t, t.TempDir(), "checks.yaml", content)
			if _, err := LoadChecks(path); err == nil {
				t.Fatal("LoadChecks accepted a malformed file")
			}
		})
	}
}

// The shipped file is the one every consumer starts from, so it must parse.
func TestShippedChecksFileParses(t *testing.T) {
	checks, err := LoadChecks("checks.yaml")
	if err != nil {
		t.Fatalf("LoadChecks on the shipped file: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("the shipped file declares no checks")
	}
	for _, c := range checks {
		if !strings.HasPrefix(c.Path, "/") {
			t.Errorf("check %q has path %q", c.Name, c.Path)
		}
	}
}
