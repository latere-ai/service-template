//go:build integration

package testsupport

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The unit tier runs with the race detector. A detector that is configured but
// not actually active is indistinguishable from a clean suite, so this tier
// runs a fixture that races and requires it to fail.

// runFixture runs the go tool against a copy of a testdata module and returns
// whether it succeeded, together with its combined output.
func runFixture(t *testing.T, fixture string, args ...string) (bool, string) {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, fixture, dir)

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	// The race detector needs cgo, and a build that disables it reports
	// "requires cgo" rather than finding the race.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func TestRaceDetectorFailsAFixtureThatRaces(t *testing.T) {
	RequireBinary(t, "go")

	ok, out := runFixture(t, "testdata/race", "test", "-race", "-count=1", "./...")
	if ok {
		t.Fatalf("the racing fixture passed under -race; the detector is not active:\n%s", out)
	}
	if !strings.Contains(out, "DATA RACE") {
		t.Fatalf("the fixture failed for a reason other than the race:\n%s", out)
	}

	// The control run pins that the fixture fails because of the detector and
	// not because the test is broken.
	ok, out = runFixture(t, "testdata/race", "test", "-count=1", "./...")
	if !ok {
		t.Fatalf("the fixture fails without the detector, so its failure proves nothing:\n%s", out)
	}
}

func TestUnitTierPassesTheRaceFlag(t *testing.T) {
	data, err := os.ReadFile(repoFile(t, "make/core.mk"))
	if err != nil {
		t.Fatalf("read the core fragment: %v", err)
	}
	if !strings.Contains(string(data), "go test -race") {
		t.Error("the make fragment runs the test tiers without -race")
	}
}
