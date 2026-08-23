// Package testsupport holds the helpers every test tier shares.
//
// Its purpose is one rule: a test tier that CI declares required cannot decide
// at run time to skip itself. A suite that skips its database tests and still
// prints "ok" reports success for work it did not do, so the mode is process
// wide, CI sets it to required, and the helper is the only place a test learns
// whether a dependency is present.
package testsupport

import (
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
)

// Mode decides what happens when a dependency is absent.
type Mode string

const (
	// Required makes a missing dependency fail the test.
	Required Mode = "required"
	// Optional makes a missing dependency skip the test and count the skip.
	Optional Mode = "optional"
)

const (
	// ModeEnv selects the mode. CI sets it to "required".
	ModeEnv = "TEST_DEPENDENCY_MODE"
	// SkipReportEnv names a file that receives one line per counted skip. The
	// CI verify job asserts the file is empty for a required tier.
	SkipReportEnv = "TEST_SKIP_REPORT"
)

// TB is the part of *testing.T the helpers use. It is an interface so the
// helpers themselves are testable: testing.TB cannot be implemented outside
// the testing package.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

// Dependency is an external service a test tier needs, and the environment
// variable that carries its address.
type Dependency struct {
	// Name identifies the dependency in the failure message, for example
	// "postgres".
	Name string
	// Env is the environment variable holding the address or connection
	// string, for example "DATABASE_URL".
	Env string
}

// Postgres is the database dependency the integration tier needs.
var Postgres = Dependency{Name: "postgres", Env: "DATABASE_URL"}

var skips atomic.Int64

// Skips reports how many dependencies were missing and skipped in this
// process. It is zero in required mode, because required mode never skips.
func Skips() int { return int(skips.Load()) }

// CurrentMode reports the configured mode. Anything other than "required"
// is optional, so a local run without the variable set behaves as optional.
func CurrentMode() Mode {
	if Mode(os.Getenv(ModeEnv)) == Required {
		return Required
	}
	return Optional
}

// Require returns the address of dep. In required mode a missing dependency
// fails the test and names it. In optional mode the test skips and the skip is
// counted.
func Require(t TB, dep Dependency) string {
	t.Helper()
	if v := os.Getenv(dep.Env); v != "" {
		return v
	}
	missing(t, dep.Name, fmt.Sprintf("%s is not set", dep.Env))
	return ""
}

// RequireBinary returns the resolved path of an executable the test needs. It
// follows the same required and optional rules as Require.
func RequireBinary(t TB, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	missing(t, name, fmt.Sprintf("%s is not on PATH: %v", name, err))
	return ""
}

// missing applies the mode. It never returns in either mode when called from a
// test, because Fatalf and Skipf both stop the test goroutine; the recorder
// used by this package's own tests returns instead, which is why every caller
// has an explicit return after it.
func missing(t TB, name, detail string) {
	t.Helper()
	if CurrentMode() == Required {
		t.Fatalf("required dependency %q is unavailable: %s (set %s=%s to skip instead)",
			name, detail, ModeEnv, Optional)
		return
	}
	skips.Add(1)
	recordSkip(t, name, detail)
	t.Skipf("optional dependency %q is unavailable: %s", name, detail)
}

// recordSkip appends the skip to the report file when one is configured. A
// skip that cannot be recorded fails the test, because a silently lost skip is
// exactly what the report exists to catch.
func recordSkip(t TB, name, detail string) {
	t.Helper()
	path := os.Getenv(SkipReportEnv)
	if path == "" {
		return
	}
	if err := appendSkip(path, name, detail); err != nil {
		t.Fatalf("record the skip in %s: %v", path, err)
	}
}

// appendSkip writes one report record. The file is opened for append so
// several packages in one test run accumulate into the same report.
func appendSkip(path, name, detail string) (err error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		// A close failure on a written file can still mean the record was
		// lost, so it replaces a successful write.
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = fmt.Fprintf(f, "%s\t%s\n", name, detail)
	return err
}
