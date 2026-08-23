package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stub is one canned answer, matched on the prefix of the rendered command.
type stub struct {
	match  string
	result Result
}

// fakeRunner answers commands from a list of stubs and records every call, so
// a test states which external commands a subcommand is allowed to issue.
type fakeRunner struct {
	stubs []stub
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, c Command) Result {
	f.calls = append(f.calls, c.String())
	for _, s := range f.stubs {
		if strings.HasPrefix(c.String(), s.match) {
			return s.result
		}
	}
	return Result{ExitCode: 127, Err: fmt.Errorf("unexpected command: %s", c)}
}

// called reports whether any recorded call starts with prefix.
func (f *fakeRunner) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// ok is a command that succeeded with output.
func ok(out string) Result { return Result{Output: out} }

// fails is a command that exited non-zero.
func fails(msg string) Result { return Result{ExitCode: 1, Err: errors.New(msg)} }

// envOf returns a getenv function over a map.
func envOf(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// writeFile creates a file inside dir, making the parent directories.
func writeFile(t *testing.T, dir, name, content string) string {
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

// copyFile duplicates a file into dir under the same base name and returns the
// copy, so a test can mutate one build file without touching the repository's.
func copyFile(t *testing.T, src, dir string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	return writeFile(t, dir, filepath.Base(src), string(data))
}

// mustContain fails the test when the text is absent.
func mustContain(t *testing.T, got, want, what string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s does not contain %q:\n%s", what, want, got)
	}
}

// problemsContain reports whether any problem holds the substring.
func problemsContain(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}
