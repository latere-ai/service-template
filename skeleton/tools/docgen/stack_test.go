//go:build integration

// The end-to-end test of the local environment runs in the integration tier.
// It needs a container engine, and a dependency that a tier can decide to skip
// at run time belongs where CI declares the dependency required.
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"example.com/service/internal/testsupport"
)

// The end-to-end test of the local environment. It runs the targets a
// contributor runs, in the order the instructions give them, because the
// distance between a clean clone and a running stack is where contributors are
// lost, and a set of targets that were only read is not evidence that the
// distance is one command.
//
// It needs a container engine that speaks compose. Without one it follows the
// dependency rule: a tier that declares the dependency required fails and names
// it, and a local run skips.
func TestTheLocalStackStartsSeedsAndTearsDown(t *testing.T) {
	engine := requireComposeEngine(t)
	project := fmt.Sprintf("svc-e2e-%d", time.Now().UnixNano())
	port := freePort(t)

	env := []string{
		"DEV_ENGINE=" + engine,
		"DEV_PROJECT=" + project,
		fmt.Sprintf("DEV_DB_PORT=%d", port),
	}
	stack := func(t *testing.T, target string) string {
		t.Helper()
		return makeOutput(t, append([]string{target}, env...)...)
	}
	t.Cleanup(func() {
		cmd := exec.Command("make", append([]string{"-C", repoRoot, "--no-print-directory", "dev-down"}, env...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("the cleanup could not remove the stack: %v\n%s", err, out)
		}
	})

	if out := stack(t, "dev-up"); !strings.Contains(out, "ready") {
		t.Fatalf("the stack did not report itself ready:\n%s", out)
	}
	// Running it again converges. A contributor who runs the command twice
	// must not have to know which state the stack was in.
	if out := stack(t, "dev-up"); !strings.Contains(out, "ready") {
		t.Fatalf("a second start did not converge:\n%s", out)
	}

	stack(t, "dev-seed")
	first := dumpSeed(t, engine, project)
	if !strings.Contains(first, "boundary") {
		t.Fatalf("the seeded data does not cover the boundary case:\n%s", first)
	}
	// Seeding twice holds the same bytes, which is what makes a screenshot or
	// a manual check comparable between contributors.
	stack(t, "dev-seed")
	if second := dumpSeed(t, engine, project); second != first {
		t.Errorf("two seed runs differ:\n%s\n%s", first, second)
	}

	stack(t, "dev-down")
	if volumes := engineOutput(t, engine, "volume", "ls", "--format", "{{.Name}}"); strings.Contains(volumes, project) {
		t.Errorf("taking the stack down left a volume behind:\n%s", volumes)
	}

	// A stack that was taken down comes back, so a broken local state is one
	// command from clean and one command from running.
	if out := stack(t, "dev-up"); !strings.Contains(out, "ready") {
		t.Fatalf("the stack did not come back after being taken down:\n%s", out)
	}
	stack(t, "dev-seed")
	if again := dumpSeed(t, engine, project); again != first {
		t.Errorf("a rebuilt stack holds different data:\n%s\n%s", first, again)
	}
}

// dumpSeed reads the seeded rows in a fixed order.
func dumpSeed(t *testing.T, engine, project string) string {
	t.Helper()
	const query = `select id || '|' || name || '|' || state || '|' || coalesce(note, '') || '|' || created_at
	               from dev_seed.example order by id`
	out := engineOutput(t, engine, "compose", "-f", repoRoot+"/docker-compose.yml", "-p", project,
		"exec", "-T", "postgres", "psql", "-U", "service", "-d", "service", "-At", "-c", query)
	if strings.TrimSpace(out) == "" {
		t.Fatal("the seeded table is empty")
	}
	return out
}

// engineOutput runs a container engine command and returns its output.
func engineOutput(t *testing.T, engine string, args ...string) string {
	t.Helper()
	cmd := exec.Command(engine, args...)
	cmd.Env = append(os.Environ(), "DEV_DB_USER=service", "DEV_DB_PASSWORD=service", "DEV_DB_NAME=service")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", engine, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// requireComposeEngine resolves a container engine that speaks compose, under
// the dependency rule.
func requireComposeEngine(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"docker", "podman"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := exec.Command(path, "compose", "version").Run(); err == nil {
			return name
		}
	}
	// No engine answered, so the dependency is absent. The helper decides
	// whether that fails the tier or skips it.
	return testsupport.RequireBinary(t, "docker")
}

// freePort reserves a port the stack can publish on, so a test run does not
// collide with a stack a contributor is already running.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("the reserved address is not a TCP address: %T", listener.Addr())
	}
	return addr.Port
}
