package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// runCommand drives the command and returns what it printed.
func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut strings.Builder
	err := run(args, &out, &errOut)
	return out.String() + errOut.String(), err
}

func TestRunRejectsNoCommandAndAnUnknownCommand(t *testing.T) {
	if out, err := runCommand(t); err == nil || !strings.Contains(out, "usage:") {
		t.Errorf("an empty invocation did not print the usage: %v, %q", err, out)
	}
	if out, err := runCommand(t, "explain"); err == nil || !strings.Contains(out, "usage:") {
		t.Errorf("an unknown command did not print the usage: %v, %q", err, out)
	}
	if _, err := runCommand(t, "check", "-nope"); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

func TestGenerateThenCheckPasses(t *testing.T) {
	dir := t.TempDir()
	out, err := runCommand(t, "generate", "-docs", dir)
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "configuration.md") || !strings.Contains(out, "api.md") {
		t.Errorf("the command did not report what it wrote: %q", out)
	}
	if out, err := runCommand(t, "docs-check", "-docs", dir); err != nil {
		t.Fatalf("docs-check: %v\n%s", err, out)
	}
}

// The repository passes its own check, which is what the pipeline runs.
func TestCheckPassesOnThisRepository(t *testing.T) {
	out, err := runCommand(t, "check",
		"-root", repoRoot,
		"-docs", filepath.Join(repoRoot, "docs"),
		"-compose", filepath.Join(repoRoot, "docker-compose.yml"),
		// Rendering is asserted by the diagram tests, under the dependency
		// rule that decides whether a missing renderer skips or fails.
		"-mermaid", "none")
	if err != nil {
		t.Fatalf("the repository fails its own documentation check: %v\n%s", err, out)
	}
}

// Every check runs, so one pass reports every problem rather than the first.
func TestCheckReportsEveryFailingCheckAtOnce(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"docs/configuration.md": "# Configuration\n\nWritten by hand.\n",
		"docs/api.md":           "# Interface reference\n\nWritten by hand.\n",
		"README.md": "# Title\n\nSee [the guide](docs/guide.md), which comes from specs/023-a.md.\n\n" +
			"```mermaid\nflowchart LR\n  a[Start --> b\n```\n",
		"docker-compose.yml": "services:\n  postgres:\n    image: postgres:latest\n",
	})
	out, err := runCommand(t, "check",
		"-root", dir,
		"-docs", filepath.Join(dir, "docs"),
		"-compose", filepath.Join(dir, "docker-compose.yml"),
		"-mermaid", "none")
	if err == nil {
		t.Fatalf("a repository with four broken checks passed:\n%s", out)
	}
	message := err.Error()
	for _, want := range []string{"out of date", "does not exist", "does not close it", "is a spec file identifier", "not pinned by digest"} {
		if !strings.Contains(message, want) {
			t.Errorf("the report does not carry %q:\n%s", want, message)
		}
	}
}
