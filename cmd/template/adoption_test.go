// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build adoption

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/service-template/internal/generator"
)

// adoptionVersion is the release the proof's build of the command reports. A
// release build carries its version in its build information; the proof sets
// the same value at link time, so the scaffold runs without -version exactly as
// the documented command does.
const adoptionVersion = "v1.0.0"

// The adoption proof. It runs the path the README documents for a developer
// starting a service, from outside this repository: the command scaffolds a
// service into an empty directory, and the service builds and passes its own
// checks, the template drift check among them. Every step is a command the
// developer runs, so a change that breaks the documented path fails here
// rather than in the first repository that follows it.
//
// The suite compiles, vets, and tests a whole generated service, which takes
// minutes and downloads the service's dependencies, so it sits behind the
// adoption build tag and runs as `make adoption`, part of `make validate`.
func TestAdoption(t *testing.T) {
	work := t.TempDir()
	bin := filepath.Join(work, "bin", "template")
	run(t, ".", "go", "build", "-ldflags", "-X main.version="+adoptionVersion, "-o", bin, ".")

	// The scaffold runs from a directory that holds no template checkout, so
	// the command can only use what it carries.
	svc := filepath.Join(work, "my-service")
	run(t, work, bin, "init",
		"-C", svc,
		"-module", "github.com/acme/my-service",
		"-name", "my-service",
		"-profile", "service",
		"-features", "frontend,database")
	declared := readText(t, filepath.Join(svc, ".template.yaml"))
	if !strings.Contains(declared, "version: "+adoptionVersion+"\n") {
		t.Fatalf("the scaffold does not record the release that made it, want version %s in:\n%s",
			adoptionVersion, declared)
	}

	// The service's gates ask git which files the repository tracks.
	run(t, svc, "git", "init", "-q")
	run(t, svc, "git", "add", "-A")

	// The checks a bare `make` runs that need nothing beyond the Go toolchain,
	// git, and make. lint needs golangci-lint installed and the frontend
	// targets need Bun; `make validate` lints and tests the same code as the
	// skeleton module.
	for _, target := range []string{
		"build", "vet", "test", "fmt-check", "lint-modernize", "lint-otel",
		"env-example-check", "settings-verify", "spec-check",
	} {
		makeTarget(t, svc, target)
	}
	// The drift check. By default it runs the declared release through the
	// module proxy, which is what the verify pipeline does; the proof's build
	// is not on the proxy, so the check runs it through the variable that
	// names another build of the command.
	dry := output(t, svc, "make", "--no-print-directory", "-n", "template-check")
	if want := "go run " + generator.CommandPath + "@" + adoptionVersion + " check"; !strings.Contains(dry, want) {
		t.Fatalf("make template-check does not run the declared release, want %q in:\n%s", want, dry)
	}
	makeTarget(t, svc, "template-check", "TEMPLATE_COMMAND="+bin)

	// A service adds a setting of its own on the first day and regenerates the
	// example environment file from the configuration struct. Neither the
	// staleness check nor the drift check may fail over it.
	addSetting(t, filepath.Join(svc, "internal", "config", "config.go"))
	makeTarget(t, svc, "env-example")
	if env := readText(t, filepath.Join(svc, ".env.example")); !strings.Contains(env, "ADOPTION_GREETING") {
		t.Fatalf("make env-example did not write the new setting into .env.example:\n%s", env)
	}
	makeTarget(t, svc, "env-example-check")
	makeTarget(t, svc, "template-check", "TEMPLATE_COMMAND="+bin)
}

// addSetting declares one more configuration field, the way a developer adds
// the first setting of their own.
func addSetting(t *testing.T, path string) {
	t.Helper()
	src := readText(t, path)
	const anchor = "type Config struct {\n"
	if !strings.Contains(src, anchor) {
		t.Fatalf("%s declares no %q to add a setting to", path, strings.TrimSpace(anchor))
	}
	field := "\t// Greeting is a setting the service adds.\n" +
		"\tGreeting string `env:\"ADOPTION_GREETING\" default:\"hello\" doc:\"Greeting the service sends.\"`\n\n"
	if err := os.WriteFile(path, []byte(strings.Replace(src, anchor, anchor+field, 1)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func makeTarget(t *testing.T, dir, target string, vars ...string) {
	t.Helper()
	run(t, dir, "make", append([]string{"--no-print-directory", target}, vars...)...)
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	output(t, dir, name, args...)
}

func output(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
