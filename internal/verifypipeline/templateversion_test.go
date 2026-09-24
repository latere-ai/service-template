// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package verifypipeline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const declaration = `template: github.com/latere-ai/service-template
version: %s
module: example.com/pay
name: pay-api
profile: service
features:
  frontend: true
  seo: false
  database: true
coverage:
  threshold: 90
`

func withDeclaration(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".template.yaml"), strings.Replace(declaration, "%s", version, 1))
	return dir
}

// Workflows ride a moving major tag while generated files are pinned exactly.
// The version gate is what stops a workflow from reading a generated file the
// repository has not received yet.
func TestTemplateVersionGate(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		minimum  string
		code     int
	}{
		{"newer than the minimum", "v1.4.0", "v1.2.0", 0},
		{"exactly the minimum", "v1.2.0", "v1.2.0", 0},
		{"a patch below", "v1.1.9", "v1.2.0", 1},
		{"a major below", "v0.9.0", "v1.0.0", 1},
		{"a pre-release of the minimum", "v1.2.0-rc.1", "v1.2.0", 1},
		{"a pre-release above the minimum", "v1.3.0-rc.1", "v1.2.0", 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := withDeclaration(t, c.declared)
			got := runScript(t, dir, "template-version.sh",
				[]string{"MIN_TEMPLATE_VERSION=" + c.minimum, "GITHUB_OUTPUT="})
			if got.Code != c.code {
				t.Fatalf("exit code %d, want %d\n%s", got.Code, c.code, got.Output)
			}
			if c.code != 0 {
				got.contains(t, "go run latere.ai/x/service-template/cmd/template@"+c.minimum+" upgrade")
			}
		})
	}
}

// The rest of the pipeline reads the declaration through these outputs, so a
// feature that is absent from the file must read as off rather than as empty.
func TestTemplateVersionOutputs(t *testing.T) {
	dir := withDeclaration(t, "v1.4.0")
	output := filepath.Join(dir, "outputs.txt")
	got := runScript(t, dir, "template-version.sh",
		[]string{"MIN_TEMPLATE_VERSION=v1.0.0", "GITHUB_OUTPUT=" + output})
	if got.Code != 0 {
		t.Fatalf("the gate failed on a current repository\n%s", got.Output)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read the outputs: %v", err)
	}
	want := []string{
		"template-version=v1.4.0",
		"name=pay-api",
		"profile=service",
		"feature-frontend=true",
		"feature-seo=false",
		"feature-i18n=false",
		"feature-database=true",
		"feature-background=false",
	}
	for _, line := range want {
		if !strings.Contains(string(data), line) {
			t.Fatalf("the outputs do not carry %q\n%s", line, data)
		}
	}
}

// A repository with no declaration is not a repository this pipeline can
// verify, and saying so early is cheaper than a job that cannot find a file.
func TestTemplateVersionWithoutDeclaration(t *testing.T) {
	got := runScript(t, t.TempDir(), "template-version.sh",
		[]string{"MIN_TEMPLATE_VERSION=v1.0.0", "GITHUB_OUTPUT="})
	if got.Code == 0 {
		t.Fatalf("the gate passed without a declaration\n%s", got.Output)
	}
	got.contains(t, "template init")
}

// Every place a pipeline tells a repository to upgrade names a command that
// runs: the release itself, fetched by `go run`, which needs no install and no
// checkout. An instruction naming a binary nothing installs, or a flag the
// command does not take, sends the reader into a second failure.
func TestUpgradeInstructionsRunTheRelease(t *testing.T) {
	const command = "go run latere.ai/x/service-template/cmd/template@"
	for _, file := range [][]string{
		{".github", "scripts", "template-version.sh"},
		{".github", "workflows", "deps.yml"},
		{".github", "workflows", "release.yml"},
	} {
		name := filepath.Join(file...)
		text := readFile(t, file...)
		if !strings.Contains(text, command) {
			t.Errorf("%s gives no upgrade instruction that runs %s<version>", name, command)
		}
		for _, stale := range []string{"template upgrade", "--to "} {
			if strings.Contains(text, stale) {
				t.Errorf("%s still tells the reader %q, which does not run", name, stale)
			}
		}
	}
}

// The verify and release pipelines accept the same lowest declaration, and the
// reference service declares a version both accept, or the example the
// repository publishes would fail the pipelines it documents.
func TestPipelinesAgreeOnTheMinimumVersion(t *testing.T) {
	verify := regexp.MustCompile(`(?m)^  MIN_TEMPLATE_VERSION: (v\S+)$`).
		FindStringSubmatch(readFile(t, ".github", "workflows", "verify.yml"))
	release := regexp.MustCompile(`minimum-template-version:\n(?:        .*\n)*?        default: (v\S+)\n`).
		FindStringSubmatch(readFile(t, ".github", "workflows", "release.yml"))
	if verify == nil || release == nil {
		t.Fatalf("a pipeline declares no minimum: verify %v, release %v", verify, release)
	}
	if verify[1] != release[1] {
		t.Fatalf("verify accepts %s and up, release accepts %s and up", verify[1], release[1])
	}
	got := runScript(t, filepath.Join(repoRoot(t), "example"), "template-version.sh",
		[]string{"MIN_TEMPLATE_VERSION=" + verify[1], "GITHUB_OUTPUT="})
	if got.Code != 0 {
		t.Fatalf("the reference service fails the minimum %s:\n%s", verify[1], got.Output)
	}
}
