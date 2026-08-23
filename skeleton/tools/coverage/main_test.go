package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProfile puts a coverage profile on disk and returns its path.
func writeProfile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestRunPassesAndFailsOnTheSameProfileSet(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, ".template.yaml")
	profile := writeProfile(t, dir, "cover.out",
		"mode: atomic\n"+
			"example.com/service/internal/a/f.go:1.1,2.2 9 1\n"+
			"example.com/service/internal/a/f.go:3.1,4.2 1 0\n")

	pass := "module: example.com/service\ncoverage:\n  threshold: 90\n"
	if err := os.WriteFile(config, []byte(pass), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}
	var out, errOut strings.Builder
	if err := run([]string{"-config", config, "-profile", profile, "-verify-packages=false"}, &out, &errOut); err != nil {
		t.Fatalf("run at the threshold failed: %v\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "overall") {
		t.Errorf("the report was not printed on success:\n%s", out.String())
	}

	fail := "module: example.com/service\ncoverage:\n  threshold: 95\n"
	if err := os.WriteFile(config, []byte(fail), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if err := run([]string{"-config", config, "-profile", profile, "-verify-packages=false"}, &out, &errOut); err == nil {
		t.Fatal("run below the threshold reported success")
	}
	if !strings.Contains(errOut.String(), "below the 95% threshold") {
		t.Errorf("the failure does not name the threshold:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "overall") {
		t.Errorf("the report was not printed on failure:\n%s", out.String())
	}
}

func TestRunMergesEveryProfileFlag(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, ".template.yaml")
	if err := os.WriteFile(config, []byte("module: example.com/service\ncoverage:\n  threshold: 100\n"), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}
	unit := writeProfile(t, dir, "unit.out",
		"mode: atomic\nexample.com/service/internal/a/f.go:1.1,2.2 5 1\n"+
			"example.com/service/internal/a/f.go:3.1,4.2 5 0\n")
	integration := writeProfile(t, dir, "integration.out",
		"mode: atomic\nexample.com/service/internal/a/f.go:1.1,2.2 5 0\n"+
			"example.com/service/internal/a/f.go:3.1,4.2 5 1\n")

	var out, errOut strings.Builder
	if err := run([]string{"-config", config, "-profile", unit, "-verify-packages=false"}, &out, &errOut); err == nil {
		t.Fatal("the unit tier alone reached 100%, so the merge proves nothing")
	}

	out.Reset()
	errOut.Reset()
	args := []string{"-config", config, "-profile", unit, "-profile", integration, "-verify-packages=false"}
	if err := run(args, &out, &errOut); err != nil {
		t.Fatalf("the merged tiers failed: %v\n%s", err, errOut.String())
	}
}

func TestRunNeedsAProfile(t *testing.T) {
	var out, errOut strings.Builder
	err := run([]string{"-verify-packages=false"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "no -profile") {
		t.Fatalf("err = %v, want a missing-profile failure", err)
	}
}

func TestRunReportsAMissingDeclaration(t *testing.T) {
	dir := t.TempDir()
	profile := writeProfile(t, dir, "cover.out",
		"mode: atomic\nexample.com/service/internal/a/f.go:1.1,2.2 10 1\n")

	var out, errOut strings.Builder
	args := []string{
		"-config", filepath.Join(dir, "absent.yaml"),
		"-profile", profile, "-verify-packages=false",
	}
	if err := run(args, &out, &errOut); err != nil {
		t.Fatalf("run with defaults failed: %v\n%s", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "not found") {
		t.Errorf("a missing declaration was not reported:\n%s", errOut.String())
	}
}

func TestRunRejectsAnUnreadableProfile(t *testing.T) {
	dir := t.TempDir()
	var out, errOut strings.Builder
	args := []string{
		"-config", filepath.Join(dir, "absent.yaml"),
		"-profile", filepath.Join(dir, "missing.out"), "-verify-packages=false",
	}
	if err := run(args, &out, &errOut); err == nil {
		t.Fatal("a missing coverage profile was treated as a pass")
	}
}

func TestProfileListFlag(t *testing.T) {
	var p profileList
	if err := p.Set("a.out"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := p.Set("b.out"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := p.String(); got != "a.out,b.out" {
		t.Errorf("String = %q", got)
	}
	if err := p.Set(""); err == nil {
		t.Error("Set accepted an empty path")
	}
}
