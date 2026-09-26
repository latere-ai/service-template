// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package verifypipeline

import (
	"path/filepath"
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
