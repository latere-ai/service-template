// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package verifypipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A scanner that cannot build the project reports zero findings and the job
// turns green. The guard exists so that an absent result is a failure with a
// message that says so, rather than a pass nobody questions.
func TestScanGuard(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "packages.txt"), "example.com/service\nexample.com/service/internal/httpx\n")
	writeFile(t, filepath.Join(dir, "empty.txt"), "")
	writeFile(t, filepath.Join(dir, "unrelated.txt"), "no packages here\n")

	cases := []struct {
		name string
		file string
		code int
		want string
	}{
		{"a report naming the packages", "packages.txt", 0, "names the analyzed packages"},
		{"an empty report", "empty.txt", 1, "scan did not run"},
		{"a report naming nothing", "unrelated.txt", 1, "scan did not run"},
		{"a missing report", "absent.txt", 1, "scan did not run"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runScript(t, dir, "scan-guard.sh", nil,
				"--tool", "vet", "--file", c.file, "--evidence", "^example.com/service")
			if got.Code != c.code {
				t.Fatalf("exit code %d, want %d\n%s", got.Code, c.code, got.Output)
			}
			got.contains(t, c.want)
		})
	}
}

// Reachability is the whole point of govulncheck: an advisory on a symbol the
// build reaches is a defect, and an advisory on an imported module whose
// vulnerable symbol is never called is information.
//
// The reports under testdata are govulncheck output captured from one module
// that depends on a version with a published advisory, in two states: the
// vulnerable symbol called, and the same package imported without calling it.
// Only the advisory records the report does not read were dropped.
func TestGovulncheckReachability(t *testing.T) {
	cases := []struct {
		name   string
		report string
		code   int
		want   string
	}{
		{"a reached symbol fails", "govulncheck-reached.json", 1, "GO-2021-0113"},
		{"the same advisory unreached passes", "govulncheck-unreached.json", 0, "not reached"},
		{"no advisory at all passes", "govulncheck-clean.json", 0, "no advisories affect this module"},
		{"a report naming no analyzed module fails", "govulncheck-nopackages.json", 1, "scan did not run"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runScript(t, t.TempDir(), "govulncheck-report.sh", nil,
				"--file", testdataPath(t, c.report))
			if got.Code != c.code {
				t.Fatalf("exit code %d, want %d\n%s", got.Code, c.code, got.Output)
			}
			got.contains(t, c.want)
		})
	}
}

// A reached advisory is silenced only by a live entry in the tracked file, and
// the summary states that it was silenced rather than hiding the row.
func TestGovulncheckSuppression(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "suppressions.yml")
	writeFile(t, file, `suppressions:
  - id: GO-2021-0113
    tool: govulncheck
    reason: the fix is in the next release of the dependency, tracked in the update queue
    expires: 2099-01-01
`)
	got := runScript(t, dir, "govulncheck-report.sh", nil,
		"--file", testdataPath(t, "govulncheck-reached.json"), "--suppressions", file)
	if got.Code != 0 {
		t.Fatalf("a live suppression did not silence the advisory\n%s", got.Output)
	}
	got.contains(t, "reached, suppressed")

	writeFile(t, file, `suppressions:
  - id: GO-2021-0113
    tool: govulncheck
    reason: the fix is in the next release of the dependency, tracked in the update queue
    expires: 2020-01-01
`)
	got = runScript(t, dir, "govulncheck-report.sh", nil,
		"--file", testdataPath(t, "govulncheck-reached.json"), "--suppressions", file)
	if got.Code == 0 {
		t.Fatalf("an expired suppression silenced a reached advisory\n%s", got.Output)
	}
}

// The summary carries the advisory table, because a pass mark alone tells a
// reviewer nothing about what the scan found and ignored.
func TestGovulncheckWritesTheSummary(t *testing.T) {
	dir := t.TempDir()
	summary := filepath.Join(dir, "summary.md")
	runScript(t, dir, "govulncheck-report.sh", []string{"GITHUB_STEP_SUMMARY=" + summary},
		"--file", testdataPath(t, "govulncheck-unreached.json"))
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	for _, want := range []string{"GO-2021-0113", "not reached", "golang.org/x/text"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the summary does not mention %q\n%s", want, data)
		}
	}
}
