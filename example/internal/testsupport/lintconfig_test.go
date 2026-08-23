//go:build integration

package testsupport

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This tier proves the generated lint configuration enforces what it lists. A
// configuration file is only a claim until a fixture shows each rule firing,
// and a rule set that silently stops firing is the failure the explicit list
// exists to prevent.
//
// It sits in the integration tier because golangci-lint takes a module-wide
// lock, which is the same reason the pre-commit hook does not run it.

// lintIssue is the part of the tool's JSON report this test reads.
type lintIssue struct {
	FromLinter string
	Pos        struct {
		Filename string
		Line     int
	}
}

// runLint copies the fixture module and the repository's own configuration
// into a scratch directory and lints it. The configuration is copied rather
// than duplicated in testdata, so the test judges the file consumers receive.
func runLint(t *testing.T) []lintIssue {
	t.Helper()
	bin := RequireBinary(t, "golangci-lint")

	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "lint"), dir)
	copyFile(t, repoFile(t, ".golangci.yml"), filepath.Join(dir, ".golangci.yml"))

	cmd := exec.Command(bin, "run", "--output.json.path", "stdout", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	// A non-zero exit is how the tool reports findings, so only a failure to
	// start the process is fatal here.
	exit, isExit := errors.AsType[*exec.ExitError](err)
	if err != nil && !isExit {
		t.Fatalf("run golangci-lint: %v", err)
	}
	// A scan that produces no report is a failed gate, never a pass.
	if len(out) == 0 {
		stderr := ""
		if isExit {
			stderr = string(exit.Stderr)
		}
		t.Fatalf("golangci-lint produced no report; the lint gate did not run: %v\n%s", err, stderr)
	}

	// The tool prints a human-readable summary after the JSON document, so the
	// decoder reads the first value and ignores the trailing text.
	var report struct{ Issues []lintIssue }
	if err := json.NewDecoder(strings.NewReader(string(out))).Decode(&report); err != nil {
		t.Fatalf("parse the lint report: %v\n%s", err, out)
	}
	return report.Issues
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// byLinter counts the findings each linter produced in one file.
func byLinter(issues []lintIssue, file string) map[string]int {
	counts := map[string]int{}
	for _, i := range issues {
		if filepath.ToSlash(i.Pos.Filename) == file {
			counts[i.FromLinter]++
		}
	}
	return counts
}

func names(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for k := range counts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEveryEnabledLinterFires drives one defect per rule through the real
// configuration. Each rule reports exactly once, so a rule that stopped firing
// and a rule that started double-reporting both fail here.
func TestEveryEnabledLinterFires(t *testing.T) {
	issues := runLint(t)
	counts := byLinter(issues, "internal/handler/handler.go")

	want := []string{"depguard", "errcheck", "ineffassign", "modernize", "sloglint"}
	got := names(counts)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("linters that fired = %v, want %v (all issues: %+v)", got, want, issues)
	}
	for _, linter := range want {
		if counts[linter] != 1 {
			t.Errorf("%s produced %d findings, want 1", linter, counts[linter])
		}
	}
	if len(issues) != len(want) {
		t.Errorf("the fixture produced %d findings in total, want %d: %+v", len(issues), len(want), issues)
	}
}

// TestScopedLoggingRuleIsAnchoredToTheRequestPath proves both directions of
// the one rule that is scoped by path. The same call fails in a handler and
// passes in the storage layer, which is what keeps the rule from being
// weakened by moving code.
func TestScopedLoggingRuleIsAnchoredToTheRequestPath(t *testing.T) {
	issues := runLint(t)

	if n := byLinter(issues, "internal/handler/handler.go")["sloglint"]; n != 1 {
		t.Errorf("sloglint findings inside the request path = %d, want 1", n)
	}
	if n := byLinter(issues, "internal/store/store.go")["sloglint"]; n != 0 {
		t.Errorf("sloglint findings outside the request path = %d, want 0", n)
	}
}

// TestConfigurationDisablesTheToolDefaults pins the property the whole file
// depends on. An additive configuration inherits whatever set the tool ships
// with, so a tool upgrade would change the rules with no diff to review.
func TestConfigurationDisablesTheToolDefaults(t *testing.T) {
	data, err := os.ReadFile(repoFile(t, ".golangci.yml"))
	if err != nil {
		t.Fatalf("read the configuration: %v", err)
	}
	if !strings.Contains(string(data), "default: none") {
		t.Error("the configuration does not start from default: none")
	}
	if !strings.Contains(string(data), "tests: true") {
		t.Error("the configuration excludes test files, which creates a second standard for the code that proves the first")
	}
	if !strings.Contains(string(data), "timeout:") {
		t.Error("the configuration sets no explicit run timeout")
	}
}
