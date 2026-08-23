package verifypipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate is the single required check, so every non-success upstream result
// must fail it. The skipped case is the one that matters: a required check
// that did not run is reported as neutral by the protection rule, and a path
// filter or a matrix condition can remove a gate from a run without anyone
// seeing it.
func TestGateVerdicts(t *testing.T) {
	cases := []struct {
		name  string
		needs string
		code  int
		want  string
	}{
		{
			name:  "every job succeeded",
			needs: `{"lint":{"result":"success"},"test":{"result":"success"}}`,
			code:  0,
			want:  "every upstream job succeeded",
		},
		{
			name:  "a job failed",
			needs: `{"lint":{"result":"failure"},"test":{"result":"success"}}`,
			code:  1,
			want:  "lint(failure)",
		},
		{
			name:  "a job was skipped",
			needs: `{"lint":{"result":"success"},"test":{"result":"skipped"}}`,
			code:  1,
			want:  "test(skipped)",
		},
		{
			name:  "a job was cancelled",
			needs: `{"lint":{"result":"cancelled"},"test":{"result":"success"}}`,
			code:  1,
			want:  "lint(cancelled)",
		},
		{
			name:  "a job reported no result",
			needs: `{"lint":{},"test":{"result":"success"}}`,
			code:  1,
			want:  "lint(missing)",
		},
		{
			name:  "no job reported",
			needs: `{}`,
			code:  1,
			want:  "the upstream result set is empty",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runScript(t, t.TempDir(), "gate.sh", []string{"NEEDS_JSON=" + c.needs})
			if got.Code != c.code {
				t.Fatalf("exit code %d, want %d\n%s", got.Code, c.code, got.Output)
			}
			got.contains(t, c.want)
		})
	}
}

// An empty document means the gate job was wired without a needs list, which
// would make it pass while judging nothing.
func TestGateWithoutResults(t *testing.T) {
	got := runScript(t, t.TempDir(), "gate.sh", []string{"NEEDS_JSON="})
	if got.Code == 0 {
		t.Fatalf("the gate passed with no upstream results\n%s", got.Output)
	}
	got.contains(t, "no upstream results")
}

// The step summary is where a reviewer reads the outcome, so the table has to
// name every job and its result.
func TestGateWritesTheSummary(t *testing.T) {
	dir := t.TempDir()
	summary := filepath.Join(dir, "summary.md")
	got := runScript(t, dir, "gate.sh", []string{
		`NEEDS_JSON={"lint":{"result":"success"},"test":{"result":"skipped"}}`,
		"GITHUB_STEP_SUMMARY=" + summary,
	})
	if got.Code == 0 {
		t.Fatalf("a skipped job passed the gate\n%s", got.Output)
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	for _, want := range []string{"`lint`", "`test`", "skipped", "Gate failed"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the summary does not mention %q\n%s", want, data)
		}
	}
}
