package testsupport

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// These tests hold the repository's git hooks to the shape `lateregate
// contract` checks: each is executable and delegates to the pinned lateregate,
// so the checks a commit and a push run are the binary's and are configured
// by .lateregate.yaml like every other gate. What the checks do is tested
// where they live, in latere.ai/x/ci-gate; what a copy here could get wrong
// is the delegation, so that is what is proved.

// repoFile locates a file at the repository root by walking up from the test's
// working directory. The same walk works in this module and in a repository
// generated from it, because the layout is fixed in both.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		candidate := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no %s above the working directory", rel)
		}
		dir = parent
	}
}

// lockingTools are commands a hook must never run itself. golangci-lint takes
// a machine-wide lock, so a hook that ran it would block a parallel build, and
// a hook that blocks a build gets disabled. The suite and the vulnerability
// scanner are slow and need the network or the whole tree, so they belong to
// the full bar in CI.
var lockingTools = []string{
	"golangci-lint",
	"staticcheck",
	"revive",
	"govulncheck",
	"go test",
	"go vet",
}

// scriptCommands returns a script's executable lines, with the shebang,
// comments, and blank lines removed. A comment naming a tool explains why the
// hook avoids it and must not be read as an invocation.
func scriptCommands(t *testing.T, source string) []string {
	t.Helper()
	var out []string
	s := bufio.NewScanner(strings.NewReader(source))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan the hook: %v", err)
	}
	return out
}

// invokedLockingTools reports every forbidden tool the script's executable
// lines name.
func invokedLockingTools(t *testing.T, source string) []string {
	t.Helper()
	var found []string
	for _, line := range scriptCommands(t, source) {
		for _, tool := range lockingTools {
			if strings.Contains(line, tool) {
				found = append(found, tool)
			}
		}
	}
	return found
}

// readHook returns a hook's source and fails unless git would run it.
func readHook(t *testing.T, rel string) string {
	t.Helper()
	path := repoFile(t, rel)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s is not executable, so git never runs it; run make hooks", rel)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// The pre-commit hook is the one delegation line, so the staged files are
// judged by the same rules and configuration as the full bar.
func TestPreCommitHookDelegatesToLateregate(t *testing.T) {
	commands := scriptCommands(t, readHook(t, ".githooks/pre-commit"))
	if !slices.Equal(commands, []string{"exec go tool lateregate hook"}) {
		t.Fatalf("the pre-commit hook runs %q; it must be exactly `exec go tool lateregate hook`, "+
			"because a check of its own is a copy no gate keeps in step with .lateregate.yaml", commands)
	}
}

// The pre-push hook reads the pushed refs once and hands them to lateregate,
// which refuses a release tag without a changelog section and lints the
// packages the push changes.
func TestPrePushHookDelegatesToLateregate(t *testing.T) {
	commands := scriptCommands(t, readHook(t, ".githooks/pre-push"))
	want := []string{"refs=$(cat)", `printf '%s\n' "$refs" | go tool lateregate prepush || exit 1`}
	if !slices.Equal(commands, want) {
		t.Fatalf("the pre-push hook runs %q, want %q", commands, want)
	}
}

func TestHooksRunNoModuleWideLinterOrTestSuite(t *testing.T) {
	for _, rel := range []string{".githooks/pre-commit", ".githooks/pre-push"} {
		if found := invokedLockingTools(t, readHook(t, rel)); len(found) != 0 {
			t.Errorf("%s invokes %v; these take a machine-wide lock or run the suite, and belong in CI", rel, found)
		}
	}
}

// TestLockingToolDetectionCanFail is the negative control for the check above.
// A check that reports clean on a script that plainly breaks the rule proves
// nothing about the script that passes it.
func TestLockingToolDetectionCanFail(t *testing.T) {
	violating := "#!/bin/sh\n# a comment about gofmt\ngolangci-lint run ./...\n"
	if found := invokedLockingTools(t, violating); len(found) == 0 {
		t.Fatal("the detector missed a plain golangci-lint invocation")
	}

	commentOnly := "#!/bin/sh\n# golangci-lint is deliberately not run here\nexec go tool lateregate hook\n"
	if found := invokedLockingTools(t, commentOnly); len(found) != 0 {
		t.Fatalf("the detector flagged a comment: %v", found)
	}
}
