package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"example.com/service/internal/testsupport"
)

// DocumentSet is the documentation every repository carries, with the audience
// each file answers to. One audience per file is what keeps a reference
// precise and a guide readable.
var DocumentSet = map[string]string{
	"README.md":             "someone deciding whether to use the service",
	"CONTRIBUTING.md":       "a contributor",
	"SECURITY.md":           "someone reporting a vulnerability",
	"docs/architecture.md":  "a new contributor",
	"docs/configuration.md": "an operator",
	"docs/api.md":           "a client builder",
	"docs/operations.md":    "whoever is on call",
}

func TestTheDocumentationSetIsPresent(t *testing.T) {
	for name := range DocumentSet {
		path := filepath.Join(repoRoot, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s is missing: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

// section returns the body of a level-two section of a document.
func section(t *testing.T, path, title string) string {
	t.Helper()
	text, err := readFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	_, rest, found := strings.Cut(text, "\n## "+title+"\n")
	if !found {
		t.Fatalf("%s has no %q section", path, title)
	}
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// shellBlocks returns the commands in the fenced shell blocks of a section.
func shellBlocks(text string) []string {
	var commands []string
	inBlock := false
	for line := range strings.SplitSeq(text, "\n") {
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			if !inBlock {
				inBlock = m[2] == "sh" || m[2] == "bash" || m[2] == "shell"
				continue
			}
			inBlock = false
			continue
		}
		if inBlock && strings.TrimSpace(line) != "" {
			commands = append(commands, strings.TrimSpace(line))
		}
	}
	return commands
}

// declaredTargets reads every target the make fragments declare.
func declaredTargets(t *testing.T) map[string]bool {
	t.Helper()
	fragments, err := filepath.Glob(filepath.Join(repoRoot, "make", "*.mk"))
	if err != nil || len(fragments) == 0 {
		t.Fatalf("find the make fragments: %v", err)
	}
	targets := map[string]bool{}
	for _, fragment := range fragments {
		text, err := readFile(fragment)
		if err != nil {
			t.Fatalf("read %s: %v", fragment, err)
		}
		for _, value := range continuedValues(text, "PHONY_TARGETS +=") {
			for name := range strings.FieldsSeq(value) {
				targets[name] = true
			}
		}
	}
	return targets
}

// Every command the quick start names must exist. A quick start that names a
// target nobody kept is the first thing a new contributor runs and the first
// thing that fails.
func TestTheQuickStartNamesRealCommands(t *testing.T) {
	targets := declaredTargets(t)
	makeCall := regexp.MustCompile(`^make\s+([a-z][a-z0-9-]*)`)

	var checked int
	for _, title := range []string{"Quick start", "Build and test"} {
		for _, command := range shellBlocks(section(t, "README.md", title)) {
			m := makeCall.FindStringSubmatch(command)
			if m == nil {
				continue
			}
			checked++
			if !targets[m[1]] {
				t.Errorf("the quick start runs %q, which no make fragment declares", command)
			}
		}
	}
	if checked == 0 {
		t.Fatal("the quick start names no make target")
	}
}

// The build half of the quick start runs here, so the instruction is tested
// rather than asserted. The dependency stack half needs a container engine and
// is exercised by the stack targets.
func TestTheQuickStartBuildsAndTheBinaryReportsItsBuild(t *testing.T) {
	binary := testsupport.RequireBinary(t, "make")
	out := t.TempDir()

	cmd := exec.Command(binary, "-C", repoRoot, "--no-print-directory", "build", "OUT_DIR="+out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, output)
	}

	entries, err := os.ReadDir(out)
	if err != nil || len(entries) == 0 {
		t.Fatalf("the build produced no binary: %v", err)
	}
	built := filepath.Join(out, entries[0].Name())

	version, err := exec.Command(built, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("the built binary does not run: %v\n%s", err, version)
	}
	if strings.TrimSpace(string(version)) == "" {
		t.Error("the built binary reports no build information")
	}
}
