package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recorder implements TB and records the outcome instead of stopping a test,
// so the helper's two modes can both be exercised in one test binary.
type recorder struct {
	fatal   string
	skip    string
	helpers int
}

func (r *recorder) Helper()                           { r.helpers++ }
func (r *recorder) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }
func (r *recorder) Skipf(format string, args ...any)  { r.skip = fmt.Sprintf(format, args...) }

func resetSkips() { skips.Store(0) }

func TestCurrentModeDefaultsToOptional(t *testing.T) {
	t.Setenv(ModeEnv, "")
	if got := CurrentMode(); got != Optional {
		t.Fatalf("CurrentMode = %q, want %q", got, Optional)
	}
	t.Setenv(ModeEnv, "nonsense")
	if got := CurrentMode(); got != Optional {
		t.Fatalf("CurrentMode = %q, want %q for an unknown value", got, Optional)
	}
	t.Setenv(ModeEnv, string(Required))
	if got := CurrentMode(); got != Required {
		t.Fatalf("CurrentMode = %q, want %q", got, Required)
	}
}

func TestRequireReturnsPresentDependency(t *testing.T) {
	t.Setenv(ModeEnv, string(Required))
	t.Setenv(Postgres.Env, "postgres://localhost/test")
	r := &recorder{}
	got := Require(r, Postgres)
	if got != "postgres://localhost/test" {
		t.Fatalf("Require = %q, want the environment value", got)
	}
	if r.fatal != "" || r.skip != "" {
		t.Fatalf("Require reported fatal=%q skip=%q for a present dependency", r.fatal, r.skip)
	}
}

func TestRequiredModeFailsAndNamesTheDependency(t *testing.T) {
	t.Setenv(ModeEnv, string(Required))
	t.Setenv(Postgres.Env, "")
	resetSkips()

	r := &recorder{}
	Require(r, Postgres)

	if r.skip != "" {
		t.Fatalf("required mode skipped: %q", r.skip)
	}
	if !strings.Contains(r.fatal, Postgres.Name) {
		t.Errorf("failure %q does not name the dependency %q", r.fatal, Postgres.Name)
	}
	if !strings.Contains(r.fatal, Postgres.Env) {
		t.Errorf("failure %q does not name the variable %q", r.fatal, Postgres.Env)
	}
	if Skips() != 0 {
		t.Errorf("Skips = %d, want 0 in required mode", Skips())
	}
}

func TestOptionalModeSkipsAndCounts(t *testing.T) {
	t.Setenv(ModeEnv, string(Optional))
	t.Setenv(Postgres.Env, "")
	resetSkips()

	r := &recorder{}
	Require(r, Postgres)

	if r.fatal != "" {
		t.Fatalf("optional mode failed: %q", r.fatal)
	}
	if !strings.Contains(r.skip, Postgres.Name) {
		t.Errorf("skip %q does not name the dependency", r.skip)
	}
	if Skips() != 1 {
		t.Errorf("Skips = %d, want 1", Skips())
	}
}

func TestOptionalModeWritesTheSkipReport(t *testing.T) {
	report := filepath.Join(t.TempDir(), "skips.txt")
	t.Setenv(ModeEnv, string(Optional))
	t.Setenv(SkipReportEnv, report)
	t.Setenv(Postgres.Env, "")
	resetSkips()

	Require(&recorder{}, Postgres)
	Require(&recorder{}, Dependency{Name: "redis", Env: "REDIS_URL"})

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read skip report: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("skip report has %d lines, want 2: %q", len(lines), string(data))
	}
	if !strings.HasPrefix(lines[0], Postgres.Name+"\t") {
		t.Errorf("first line %q does not name %q", lines[0], Postgres.Name)
	}
	if !strings.HasPrefix(lines[1], "redis\t") {
		t.Errorf("second line %q does not name redis", lines[1])
	}
}

func TestRequiredModeWritesNoSkipReport(t *testing.T) {
	report := filepath.Join(t.TempDir(), "skips.txt")
	t.Setenv(ModeEnv, string(Required))
	t.Setenv(SkipReportEnv, report)
	t.Setenv(Postgres.Env, "")
	resetSkips()

	Require(&recorder{}, Postgres)

	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Fatalf("skip report exists in required mode: err=%v", err)
	}
}

func TestRequireBinary(t *testing.T) {
	t.Setenv(ModeEnv, string(Required))
	r := &recorder{}
	if got := RequireBinary(r, "go"); got == "" {
		t.Fatalf("RequireBinary(go) = %q with fatal %q, want a path", got, r.fatal)
	}

	resetSkips()
	t.Setenv(ModeEnv, string(Optional))
	r = &recorder{}
	RequireBinary(r, "a-binary-that-does-not-exist")
	if !strings.Contains(r.skip, "a-binary-that-does-not-exist") {
		t.Fatalf("skip %q does not name the binary", r.skip)
	}
	if Skips() != 1 {
		t.Errorf("Skips = %d, want 1", Skips())
	}
}

func TestUnwritableSkipReportFails(t *testing.T) {
	t.Setenv(ModeEnv, string(Optional))
	t.Setenv(SkipReportEnv, filepath.Join(t.TempDir(), "missing-dir", "skips.txt"))
	t.Setenv(Postgres.Env, "")
	resetSkips()

	r := &recorder{}
	Require(r, Postgres)
	if r.fatal == "" {
		t.Fatal("an unwritable skip report did not fail the test")
	}
}
