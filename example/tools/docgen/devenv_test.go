package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/reference-service/internal/testsupport"
)

// makeOutput runs one make target in the repository and returns what it wrote.
func makeOutput(t *testing.T, args ...string) string {
	t.Helper()
	return makeWithin(t, 10*time.Minute, args...)
}

// makeWithin is makeOutput with a deadline. A target that should return and
// does not is a failure with a name, not a test run that hangs until the
// suite times out.
func makeWithin(t *testing.T, limit time.Duration, args ...string) string {
	t.Helper()
	binary := testsupport.RequireBinary(t, "make")
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, append([]string{"-C", repoRoot, "--no-print-directory"}, args...)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("make %s did not return within %s:\n%s", strings.Join(args, " "), limit, out)
	}
	if err != nil {
		t.Fatalf("make %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// ports reads the derived ports of a project name.
func ports(t *testing.T, project string) map[string]int {
	t.Helper()
	out := makeOutput(t, "-f", "make/dev.mk", "dev-ports", "DEV_PROJECT="+project)
	values := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "DEV_PROJECT" {
			continue
		}
		port, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("%s is not a port number: %q", key, value)
		}
		values[key] = port
	}
	if len(values) != 3 {
		t.Fatalf("want three ports, got %v from:\n%s", values, out)
	}
	return values
}

// Two checkouts must run at the same time, so the ports follow the project
// rather than being fixed. The same project must also keep its ports, or a
// contributor's bookmarks and proxy settings break on every run.
func TestPortsAreDerivedFromTheProjectAndAreStable(t *testing.T) {
	first := ports(t, "alpha")
	if second := ports(t, "alpha"); !samePorts(first, second) {
		t.Fatalf("the same project produced different ports: %v then %v", first, second)
	}

	other := ports(t, "beta")
	for key, port := range first {
		if other[key] == port {
			t.Errorf("%s collides between two projects at %d", key, port)
		}
	}

	seen := map[int]string{}
	for key, port := range first {
		if port < 1024 || port > 65535 {
			t.Errorf("%s is outside the usable range: %d", key, port)
		}
		if previous, ok := seen[port]; ok {
			t.Errorf("%s and %s bind the same port %d", previous, key, port)
		}
		seen[port] = key
	}
}

func samePorts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, port := range a {
		if b[key] != port {
			return false
		}
	}
	return true
}

// An override wins, because a contributor with a port already in use needs a
// way out that is not editing the fragment.
func TestAnExplicitPortOverridesTheDerivedOne(t *testing.T) {
	out := makeOutput(t, "-f", "make/dev.mk", "dev-ports", "DEV_PROJECT=alpha", "DEV_HTTP_PORT=31000")
	if !strings.Contains(out, "DEV_HTTP_PORT=31000") {
		t.Fatalf("the override was ignored:\n%s", out)
	}
}

// The recipes are read rather than run, so the rules they carry are asserted
// without a container engine: the stack is namespaced, and taking it down
// removes the volumes as well as the containers.
func TestTheStackIsNamespacedAndTakenDownWithItsVolumes(t *testing.T) {
	up := makeOutput(t, "-n", "dev-up", "DEV_PROJECT=alpha")
	if !strings.Contains(up, "-p alpha") {
		t.Errorf("the stack is not namespaced by project:\n%s", up)
	}
	if !strings.Contains(up, "up -d") {
		t.Errorf("dev-up does not start the stack in the background:\n%s", up)
	}

	down := makeOutput(t, "-n", "dev-down", "DEV_PROJECT=alpha")
	if !strings.Contains(down, "--volumes") {
		t.Errorf("dev-down leaves the volumes behind:\n%s", down)
	}
	if !strings.Contains(down, "-p alpha") {
		t.Errorf("dev-down does not address the namespaced stack:\n%s", down)
	}
}

// One command reaches a serving stack, so the target chains the steps a
// contributor would otherwise run by hand and remember in the wrong order.
func TestDevChainsTheStepsToAServingStack(t *testing.T) {
	// The deadline also holds the rule that a dry run starts nothing: a recipe
	// line that names MAKE is executed even by a dry run, so a watch loop or a
	// development server on such a line would never return.
	plan := makeWithin(t, time.Minute, "-n", "dev", "DEV_PROJECT=alpha")
	for _, want := range []string{
		"up -d",                 // the dependency stack
		"DATABASE_URL=postgres", // the connection the migration step reads
		"psql",                  // the development data set
		"build",                 // the backend, built the way the pipeline builds it
		"bun run dev",           // the frontend development server
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("the plan of `make dev` does not carry %q:\n%s", want, plan)
		}
	}
}

// volatileSQL are the expressions that would make two seed runs differ.
var volatileSQL = []string{
	"now()", "current_timestamp", "current_date", "clock_timestamp",
	"random()", "gen_random_uuid", "uuid_generate", "nextval",
}

// seedScript reads the development data set out of the make fragment.
func seedScript(t *testing.T) string {
	t.Helper()
	text, err := readFile(repoRoot + "/make/dev.mk")
	if err != nil {
		t.Fatalf("read the fragment: %v", err)
	}
	_, rest, found := strings.Cut(text, "\ndefine DEV_SEED_SQL\n")
	if !found {
		t.Fatal("the fragment carries no seed script")
	}
	script, _, found := strings.Cut(rest, "\nendef")
	if !found {
		t.Fatal("the seed script is never closed")
	}
	if strings.TrimSpace(script) == "" {
		t.Fatal("the seed script is empty")
	}
	return strings.TrimSpace(script)
}

// Deterministic seeding is what makes a screenshot and a manual check
// comparable between contributors, so the script holds literals and no clock.
func TestTheSeedDataIsDeterministic(t *testing.T) {
	script := strings.ToLower(seedScript(t))
	for _, expression := range volatileSQL {
		if strings.Contains(script, expression) {
			t.Errorf("the seed script calls %s, so two runs differ", expression)
		}
	}
	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2}t\d{2}:\d{2}:\d{2}z`).MatchString(script) {
		t.Error("the seed script has no literal timestamp")
	}
	truncate := strings.Index(script, "truncate")
	insert := strings.Index(script, "insert")
	switch {
	case truncate < 0:
		t.Error("the seed script does not clear the table, so reseeding accumulates rows")
	case insert < 0:
		t.Error("the seed script inserts nothing")
	case truncate > insert:
		t.Error("the seed script inserts before it clears, so reseeding accumulates rows")
	}
}

// The data set covers the states an interface has to render, which is what
// makes seeded data useful for a manual check rather than only present.
func TestTheSeedDataCoversTheStatesTheInterfaceRenders(t *testing.T) {
	script := strings.ToLower(seedScript(t))
	for _, state := range []string{"empty", "typical", "boundary"} {
		if !strings.Contains(script, state) {
			t.Errorf("the seed script covers no %s case", state)
		}
	}
}

// Every target the fragment defines is declared, or a bare `make` in a
// repository with a file of that name runs the file rule instead of the target.
func TestEveryDevTargetIsDeclaredPhony(t *testing.T) {
	text, err := readFile(repoRoot + "/make/dev.mk")
	if err != nil {
		t.Fatalf("read the fragment: %v", err)
	}
	declared := map[string]bool{}
	for name := range strings.FieldsSeq(strings.Join(continuedValues(text, "PHONY_TARGETS +="), " ")) {
		declared[name] = true
	}
	defined := regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*):`).FindAllStringSubmatch(text, -1)
	if len(defined) == 0 {
		t.Fatal("the fragment defines no target")
	}
	for _, m := range defined {
		if !declared[m[1]] {
			t.Errorf("the target %s is not in PHONY_TARGETS", m[1])
		}
	}
}

// continuedValues reads an assignment that continues over several lines.
func continuedValues(text, prefix string) []string {
	var values []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		for strings.HasSuffix(value, "\\") && i+1 < len(lines) {
			i++
			value = strings.TrimSuffix(value, "\\") + " " + strings.TrimSpace(lines[i])
		}
		values = append(values, value)
	}
	return values
}
