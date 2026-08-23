package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the gate against a real module on disk, because the rule
// that a package with no test file must still be counted depends on the go
// tool's view of the module and not on the profile alone.

// writeModule lays out a throwaway module and makes it the working directory.
func writeModule(t *testing.T, module string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module " + module + "\n\ngo 1.27.0\n"
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Chdir(dir)
	return dir
}

func TestPackageWithoutTestsFailsTheGateAndExclusionClearsIt(t *testing.T) {
	dir := writeModule(t, "verify.example", map[string]string{
		"a/a.go": "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go": "package b\n\nfunc B() int { return 2 }\n",
	})
	profile := writeProfile(t, dir, "cover.out",
		"mode: atomic\nverify.example/a/a.go:1.1,2.2 4 1\n")

	// The declaration names no module, so the gate reads it from the module it
	// runs in.
	config := filepath.Join(dir, ".template.yaml")
	if err := os.WriteFile(config, []byte("coverage:\n  threshold: 90\n"), 0o644); err != nil {
		t.Fatalf("write the declaration: %v", err)
	}

	var out, errOut strings.Builder
	if err := run([]string{"-config", config, "-profile", profile}, &out, &errOut); err == nil {
		t.Fatalf("the gate passed with an unmeasured package:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "package b produced no coverage data") {
		t.Errorf("the failure does not name the unmeasured package:\n%s", errOut.String())
	}

	if err := os.WriteFile(config, []byte("coverage:\n  threshold: 90\n  exclude:\n    - b\n"), 0o644); err != nil {
		t.Fatalf("write the declaration: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if err := run([]string{"-config", config, "-profile", profile}, &out, &errOut); err != nil {
		t.Fatalf("the gate failed with the package excluded: %v\n%s", err, errOut.String())
	}
}

func TestModulePathIsReadFromTheModuleTheGateRunsIn(t *testing.T) {
	writeModule(t, "read.example", map[string]string{
		"a/a.go": "package a\n\nfunc A() int { return 1 }\n",
	})
	got, err := currentModule()
	if err != nil {
		t.Fatalf("currentModule: %v", err)
	}
	if got != "read.example" {
		t.Fatalf("currentModule = %q, want read.example", got)
	}

	pkgs, err := modulePackages(got)
	if err != nil {
		t.Fatalf("modulePackages: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0] != "a" {
		t.Fatalf("modulePackages = %v, want [a]", pkgs)
	}
}

// TestOutsideAModuleFailsRatherThanReportingNothing pins the rule that a gate
// which cannot run is a failure. Without it a misconfigured job reports zero
// problems and a green check.
func TestOutsideAModuleFailsRatherThanReportingNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := currentModule(); err == nil {
		t.Error("currentModule succeeded outside a module")
	}
	if _, err := modulePackages("anything.example"); err == nil {
		t.Error("modulePackages succeeded outside a module")
	}

	profile := writeProfile(t, dir, "cover.out",
		"mode: atomic\nanything.example/a/a.go:1.1,2.2 4 1\n")
	var out, errOut strings.Builder
	if err := run([]string{"-config", filepath.Join(dir, "absent.yaml"), "-profile", profile}, &out, &errOut); err == nil {
		t.Error("the gate passed outside a module")
	}
}

func TestReadDeclarationReportsAnUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ReadDeclaration(dir); err == nil {
		t.Fatal("ReadDeclaration accepted a directory")
	}
}

func TestEmptyExclusionEntriesAreIgnored(t *testing.T) {
	var d Declaration
	d.Coverage.Exclude = []string{"", "   ", "cmd/"}
	if d.IsExcluded("internal/store") {
		t.Error("an empty exclusion entry matched a package")
	}
	if !d.IsExcluded("cmd/reference-service") {
		t.Error("a directory entry did not match a package under it")
	}
}

func TestPercentOfAnEmptyReportIsZeroNotFull(t *testing.T) {
	var r Report
	if got := r.Percent(); got != 0 {
		t.Fatalf("Percent of an empty report = %v, want 0", got)
	}
}

// failingWriter reports an error on every write, which is how the report path
// learns that a broken output stream is surfaced rather than swallowed.
type failingWriter struct{}

var errWrite = errors.New("write failed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestWriteSurfacesAnOutputError(t *testing.T) {
	r := Report{
		Threshold: 90,
		Packages:  []PackageCoverage{{Path: "a", Statements: 4, Covered: 4}},
	}
	if err := r.Write(failingWriter{}); err == nil {
		t.Fatal("Write hid an output error")
	}
}

func TestRunSurfacesAnOutputError(t *testing.T) {
	dir := t.TempDir()
	profile := writeProfile(t, dir, "cover.out",
		"mode: atomic\ngithub.com/example/reference-service/a/a.go:1.1,2.2 4 1\n")
	var errOut strings.Builder
	err := run([]string{
		"-config", filepath.Join(dir, "absent.yaml"),
		"-profile", profile,
		"-verify-packages=false",
	}, failingWriter{}, &errOut)
	if err == nil {
		t.Fatal("run hid an output error")
	}
	if !strings.Contains(err.Error(), "write the coverage report") {
		t.Fatalf("error = %v, want the report write to be named", err)
	}
}

// A package that declares no function with a body cannot appear in a coverage
// profile, so listing it as unmeasured produces a finding no test can clear.
// The dependency-pinning package in the skeleton is exactly that shape.
func TestPackagesWithoutStatementsAreNotListed(t *testing.T) {
	dir := t.TempDir()
	imports := filepath.Join(dir, "imports.go")
	if err := os.WriteFile(imports, []byte("package deps\n\nimport _ \"fmt\"\n"), 0o644); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
	code := filepath.Join(dir, "code.go")
	if err := os.WriteFile(code, []byte("package deps\n\nfunc used() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}

	only, err := hasStatements(dir, []string{"imports.go"})
	if err != nil {
		t.Fatalf("hasStatements: %v", err)
	}
	if only {
		t.Error("a package of blank imports was reported as measurable")
	}

	both, err := hasStatements(dir, []string{"imports.go", "code.go"})
	if err != nil {
		t.Fatalf("hasStatements: %v", err)
	}
	if !both {
		t.Error("a package that declares a function body was reported as unmeasurable")
	}

	if _, err := hasStatements(dir, []string{"absent.go"}); err == nil {
		t.Error("a missing file was read without an error")
	}
}
