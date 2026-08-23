package testsupport

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the repository's own pre-commit hook. The hook is a
// gate, and a gate nobody proves can fail is a gate that protects nothing, so
// every rule it enforces is driven here against a throwaway repository.

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

// hookPath is the pre-commit hook at the repository root.
func hookPath(t *testing.T) string {
	t.Helper()
	return repoFile(t, ".githooks/pre-commit")
}

// lockingTools are binaries the hook must never run. golangci-lint takes a
// module-wide lock, so a hook that ran it would block a parallel build, and a
// hook that blocks a build gets disabled. The test tier and the vulnerability
// scanner are here for the same reason: they are slow, and they run in CI.
var lockingTools = []string{
	"golangci-lint",
	"staticcheck",
	"revive",
	"govulncheck",
	"go test",
	"go vet",
}

// scriptCommands returns the hook's executable lines, with comments and blank
// lines removed. A comment naming a tool explains why the hook avoids it and
// must not be read as an invocation.
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

func TestPreCommitHookRunsNoModuleWideLinterOrTestSuite(t *testing.T) {
	data, err := os.ReadFile(hookPath(t))
	if err != nil {
		t.Fatalf("read the hook: %v", err)
	}
	if found := invokedLockingTools(t, string(data)); len(found) != 0 {
		t.Fatalf("the pre-commit hook invokes %v; these take a module-wide lock or run the suite, and belong in CI", found)
	}
}

// TestLockingToolDetectionCanFail is the negative control for the check above.
// A check that reports clean on a script that plainly breaks the rule proves
// nothing about the script that passes it.
func TestLockingToolDetectionCanFail(t *testing.T) {
	violating := "#!/usr/bin/env bash\n# a comment about gofmt\ngolangci-lint run ./...\n"
	if found := invokedLockingTools(t, violating); len(found) == 0 {
		t.Fatal("the detector missed a plain golangci-lint invocation")
	}

	commentOnly := "#!/usr/bin/env bash\n# golangci-lint is deliberately not run here\ngofmt -l .\n"
	if found := invokedLockingTools(t, commentOnly); len(found) != 0 {
		t.Fatalf("the detector flagged a comment: %v", found)
	}
}

// hookRepo is a throwaway git repository with the hook installed.
type hookRepo struct {
	dir  string
	hook string
}

// newHookRepo builds a repository the hook can run against. The hook is never
// installed into the repository this test runs in, because core.hooksPath is
// repository-wide configuration and a test must not change the developer's
// working checkout.
func newHookRepo(t *testing.T) *hookRepo {
	t.Helper()
	RequireBinary(t, "git")
	RequireBinary(t, "go")
	RequireBinary(t, "gofmt")

	dir := t.TempDir()
	r := &hookRepo{dir: dir, hook: filepath.Join(dir, ".githooks", "pre-commit")}

	r.git(t, "init", "-q", "-b", "main")
	r.git(t, "config", "user.email", "test@example.com")
	r.git(t, "config", "user.name", "test")

	r.write(t, "go.mod", "module fixture.example\n\ngo 1.27.0\n")

	source, err := os.ReadFile(hookPath(t))
	if err != nil {
		t.Fatalf("read the hook: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.hook), 0o755); err != nil {
		t.Fatalf("create the hook directory: %v", err)
	}
	if err := os.WriteFile(r.hook, source, 0o755); err != nil {
		t.Fatalf("install the hook: %v", err)
	}
	return r
}

// env isolates the repository from the developer's git configuration, so a
// global hooksPath or template directory cannot change the result.
func (r *hookRepo) env() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
}

func (r *hookRepo) git(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = r.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r *hookRepo) write(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the directory for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func (r *hookRepo) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// stage writes a file and adds it to the index, which is the state the hook
// judges.
func (r *hookRepo) stage(t *testing.T, name, body string) {
	t.Helper()
	r.write(t, name, body)
	r.git(t, "add", "--", name)
}

// run executes the hook and returns its exit code and its combined output.
func (r *hookRepo) run(t *testing.T) (int, string) {
	t.Helper()
	cmd := exec.Command(r.hook)
	cmd.Dir = r.dir
	cmd.Env = r.env()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("run the hook: %v\n%s", err, out)
	return 0, ""
}

const formattedGo = "package fixture\n\nfunc Formatted() int {\n\treturn 1\n}\n"

// unformattedGo differs from gofmt output by indentation only, so gofmt is the
// only rule it breaks.
const unformattedGo = "package fixture\n\nfunc Unformatted() int {\n        return 1\n}\n"

func TestPreCommitHookAcceptsCleanContent(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "go.mod", "module fixture.example\n\ngo 1.27.0\n")
	r.stage(t, "clean.go", formattedGo)

	code, out := r.run(t)
	if code != 0 {
		t.Fatalf("the hook rejected clean content with exit %d:\n%s", code, out)
	}
}

func TestPreCommitHookRejectsUnformattedGoAndNamesTheFile(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "go.mod", "module fixture.example\n\ngo 1.27.0\n")
	r.stage(t, "bad.go", unformattedGo)

	code, out := r.run(t)
	if code == 0 {
		t.Fatalf("the hook accepted an unformatted staged file:\n%s", out)
	}
	if !strings.Contains(out, "bad.go") {
		t.Errorf("the failure does not name the file:\n%s", out)
	}
	if !strings.Contains(out, "gofmt") {
		t.Errorf("the failure does not name the rule:\n%s", out)
	}
}

// TestPreCommitHookNeverRewritesTheTree pins the reporting contract. go fix
// has fixers that emit rewrites which do not compile and fixers that apply
// partially and re-propose, so the hook reports and fails, and the developer
// applies the change.
func TestPreCommitHookNeverRewritesTheTree(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "go.mod", "module fixture.example\n\ngo 1.27.0\n")
	// A counted loop over a constant is the idiom go fix proposes to rewrite
	// as a range over an integer.
	stale := "package fixture\n\nfunc Stale() int {\n\ttotal := 0\n\tfor i := 0; i < 3; i++ {\n\t\ttotal += i\n\t}\n\treturn total\n}\n"
	r.stage(t, "stale.go", stale)
	r.stage(t, "bad.go", unformattedGo)

	code, out := r.run(t)
	if code == 0 {
		t.Fatalf("the hook accepted content it should reject:\n%s", out)
	}
	if got := r.read(t, "stale.go"); got != stale {
		t.Errorf("the hook rewrote stale.go:\n%s", got)
	}
	if got := r.read(t, "bad.go"); got != unformattedGo {
		t.Errorf("the hook rewrote bad.go:\n%s", got)
	}
}

func TestPreCommitHookReportsGoFixRewrites(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "go.mod", "module fixture.example\n\ngo 1.27.0\n")
	r.stage(t, "stale.go", "package fixture\n\nfunc Stale() int {\n\ttotal := 0\n\tfor i := 0; i < 3; i++ {\n\t\ttotal += i\n\t}\n\treturn total\n}\n")

	code, out := r.run(t)
	if code == 0 {
		t.Fatalf("the hook accepted a package go fix proposes to rewrite:\n%s", out)
	}
	if !strings.Contains(out, "go fix") {
		t.Errorf("the failure does not name go fix:\n%s", out)
	}
}

func TestPreCommitHookRejectsTrailingWhitespace(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "notes.md", "a line with a trailing space \nand a clean one\n")

	code, out := r.run(t)
	if code == 0 {
		t.Fatalf("the hook accepted trailing whitespace:\n%s", out)
	}
	if !strings.Contains(out, "notes.md") {
		t.Errorf("the failure does not name the file:\n%s", out)
	}
}

func TestPreCommitHookRejectsAMissingFinalNewline(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "notes.md", "no newline at the end")

	code, out := r.run(t)
	if code == 0 {
		t.Fatalf("the hook accepted a file with no final newline:\n%s", out)
	}
	if !strings.Contains(out, "notes.md") {
		t.Errorf("the failure does not name the file:\n%s", out)
	}
}

func TestPreCommitHookPassesWithNothingStaged(t *testing.T) {
	r := newHookRepo(t)
	code, out := r.run(t)
	if code != 0 {
		t.Fatalf("the hook failed with an empty index, exit %d:\n%s", code, out)
	}
}

// TestPreCommitHookIgnoresDeletedPaths guards the case where a staged path no
// longer exists: reading its blob would fail and the hook must not treat that
// as a formatting problem.
func TestPreCommitHookIgnoresDeletedPaths(t *testing.T) {
	r := newHookRepo(t)
	r.stage(t, "go.mod", "module fixture.example\n\ngo 1.27.0\n")
	r.stage(t, "gone.go", formattedGo)
	r.git(t, "commit", "-q", "--no-verify", "-m", "seed")
	r.git(t, "rm", "-q", "--", "gone.go")

	code, out := r.run(t)
	if code != 0 {
		t.Fatalf("the hook failed on a deletion, exit %d:\n%s", code, out)
	}
}
