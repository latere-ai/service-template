package verifypipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The summary is where a reviewer reads the result, so it carries the count
// per linter rather than a pass mark.
//
// The reports under testdata are golangci-lint output captured from a module
// with real findings and from the same module with them fixed, so the shape
// the script reads is the shape the tool writes.
func TestLintSummary(t *testing.T) {
	dir := t.TempDir()
	summary := filepath.Join(dir, "summary.md")
	got := runScript(t, dir, "lint-summary.sh", []string{"GITHUB_STEP_SUMMARY=" + summary},
		"--file", testdataPath(t, "golangci-findings.json"))
	if got.Code == 0 {
		t.Fatalf("a report with findings passed\n%s", got.Output)
	}
	got.contains(t, "6 linters enabled, 3 findings")

	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	for _, want := range []string{"| `errcheck` | 2 |", "| `ineffassign` | 1 |"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the summary does not carry %q\n%s", want, data)
		}
	}

	got = runScript(t, dir, "lint-summary.sh", nil,
		"--file", testdataPath(t, "golangci-clean.json"))
	if got.Code != 0 {
		t.Fatalf("a clean report failed\n%s", got.Output)
	}
}

// A lint run that ends before the analysis writes no report, and a missing
// report is a failed gate rather than a clean one.
func TestLintSummaryWithoutAReport(t *testing.T) {
	dir := t.TempDir()
	got := runScript(t, dir, "lint-summary.sh", nil, "--file", filepath.Join(dir, "absent.json"))
	if got.Code == 0 {
		t.Fatalf("a missing report passed\n%s", got.Output)
	}
	got.contains(t, "scan did not run")

	writeFile(t, filepath.Join(dir, "partial.json"), `{"Issues":[]}`)
	got = runScript(t, dir, "lint-summary.sh", nil, "--file", filepath.Join(dir, "partial.json"))
	if got.Code == 0 {
		t.Fatalf("a report naming no linter passed\n%s", got.Output)
	}
	got.contains(t, "names no linter")
}

// Bundle size regresses one dependency at a time, so the summary states the
// change against the default branch and not only the current number.
func TestBundleSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "index.html"), strings.Repeat("a", 100))
	writeFile(t, filepath.Join(dir, "dist", "assets", "app-1a2b.js"), strings.Repeat("b", 400))
	writeFile(t, filepath.Join(dir, "baseline.json"), `{"total": 400, "files": []}`)
	summary := filepath.Join(dir, "summary.md")

	got := runScript(t, dir, "bundle-size.sh", []string{"GITHUB_STEP_SUMMARY=" + summary},
		"--dist", "dist", "--out", "size.json", "--baseline", "baseline.json")
	if got.Code != 0 {
		t.Fatalf("measuring the bundle failed\n%s", got.Output)
	}
	got.contains(t, "500 bytes, +100 bytes against the baseline 400 (+25.00%)")

	measured, err := os.ReadFile(filepath.Join(dir, "size.json"))
	if err != nil {
		t.Fatalf("read the measurement: %v", err)
	}
	for _, want := range []string{`"total": 500`, `"path": "assets/app-1a2b.js"`, `"bytes": 400`} {
		if !strings.Contains(string(measured), want) {
			t.Fatalf("the measurement does not carry %q\n%s", want, measured)
		}
	}

	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	if !strings.Contains(string(data), "**500**") {
		t.Fatalf("the summary does not carry the total\n%s", data)
	}
}

// A build that produced nothing must not report a bundle of zero bytes as an
// improvement.
func TestBundleSizeWithoutABuild(t *testing.T) {
	dir := t.TempDir()
	got := runScript(t, dir, "bundle-size.sh", nil, "--dist", "dist", "--out", "size.json")
	if got.Code == 0 {
		t.Fatalf("a missing bundle passed\n%s", got.Output)
	}
	got.contains(t, "no bundle at dist")
}
