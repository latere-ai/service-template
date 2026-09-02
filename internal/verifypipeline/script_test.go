// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package verifypipeline

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the checkout root, which is where the workflow and its scripts
// live relative to this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	return root
}

func script(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "scripts", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the pipeline script %s is missing: %v", name, err)
	}
	return path
}

// result is what a script run produced. Output holds standard output and
// standard error together, because a script writes its verdict to one and its
// reason to the other and a test reads both.
type result struct {
	Output string
	Code   int
}

func (r result) contains(t *testing.T, want string) {
	t.Helper()
	if !strings.Contains(r.Output, want) {
		t.Fatalf("the output does not mention %q\n%s", want, r.Output)
	}
}

// runScript runs one pipeline script. dir is the working directory, env holds
// extra KEY=VALUE entries, and the exit code is returned rather than asserted
// so a test can name the outcome it expects.
func runScript(t *testing.T, dir, name string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command("sh", append([]string{script(t, name)}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("run %s: %v\n%s", name, err, out)
	}
	return result{Output: string(out), Code: code}
}

// writeFile creates a file and the directories above it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// testdataPath is a recorded scanner report used as a script input.
func testdataPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "internal", "verifypipeline", "testdata", name)
}

// Every pipeline script must parse. A syntax error in a shell step is found at
// the moment the gate runs otherwise, which is the worst moment to find it.
func TestScriptsParse(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	found := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sh") {
			continue
		}
		found++
		out, err := exec.Command("sh", "-n", filepath.Join(dir, entry.Name())).CombinedOutput()
		if err != nil {
			t.Errorf("%s does not parse: %v\n%s", entry.Name(), err, out)
		}
	}
	if found == 0 {
		t.Fatal("the pipeline ships no script")
	}
}
