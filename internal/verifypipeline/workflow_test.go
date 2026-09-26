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

func readFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

var jobHeader = regexp.MustCompile(`^  ([a-z][a-z0-9-]*):$`)

// jobs splits the workflow into one block per job, keeping declaration order.
// The workflow is read as text rather than as a parsed document because the
// module depends on nothing outside the standard library.
func jobs(t *testing.T, workflow string) ([]string, map[string]string) {
	t.Helper()
	var order []string
	blocks := map[string]string{}
	inJobs := false
	current := ""
	var block []string
	flush := func() {
		if current != "" {
			blocks[current] = strings.Join(block, "\n")
		}
		block = nil
	}
	for line := range strings.SplitSeq(workflow, "\n") {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if m := jobHeader.FindStringSubmatch(line); m != nil {
			flush()
			current = m[1]
			order = append(order, current)
			continue
		}
		if current != "" {
			block = append(block, line)
		}
	}
	flush()
	if len(order) == 0 {
		t.Fatal("the workflow declares no job")
	}
	return order, blocks
}

// needsOf reads the needs list of one job in either the inline or the single
// value spelling.
func needsOf(t *testing.T, block string) []string {
	t.Helper()
	for line := range strings.SplitSeq(block, "\n") {
		trimmed := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(trimmed, "needs:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		rest = strings.TrimPrefix(rest, "[")
		rest = strings.TrimSuffix(rest, "]")
		var names []string
		for name := range strings.SplitSeq(rest, ",") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

// The gate is the single required check, so a job missing from its needs list
// is a gate nobody judges. The build job is downstream of the gate and is the
// one job the list leaves out.
func TestGateJudgesEveryUpstreamJob(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	order, blocks := jobs(t, workflow)

	gate, ok := blocks["gate"]
	if !ok {
		t.Fatal("the workflow declares no gate job")
	}
	judged := map[string]bool{}
	for _, name := range needsOf(t, gate) {
		judged[name] = true
	}
	for _, name := range order {
		if name == "gate" || name == "build" {
			continue
		}
		if !judged[name] {
			t.Errorf("the gate does not judge the %s job, so that job can fail silently", name)
		}
	}
	if !strings.Contains(gate, "if: always()") {
		t.Error("the gate must run with if: always(), or a failed upstream job skips it")
	}
	if !strings.Contains(gate, "gate.sh") {
		t.Error("the gate job must run the gate script that judges the results")
	}
}

// The build job produces what the release pipeline consumes, and it must sit
// after the gate so the released artifact is the verified one.
func TestBuildFollowsTheGate(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	_, blocks := jobs(t, workflow)
	build, ok := blocks["build"]
	if !ok {
		t.Fatal("the workflow declares no build job")
	}
	found := false
	for _, name := range needsOf(t, build) {
		if name == "gate" {
			found = true
		}
	}
	if !found {
		t.Error("the build job must need the gate, or it builds from unverified code")
	}
	for _, want := range []string{"actions/upload-artifact", "binary-artifact", "bundle-artifact"} {
		if !strings.Contains(build, want) {
			t.Errorf("the build job does not carry %q", want)
		}
	}
	if !strings.Contains(workflow, "value: ${{ jobs.build.outputs.binary-artifact }}") {
		t.Error("the workflow must publish the artifact names, or the release pipeline cannot find them")
	}
}

// Feature selection never skips a job, because a skipped job fails the gate.
// A job that a feature switches off reports success after saying so.
func TestNoJobIsConditional(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	order, blocks := jobs(t, workflow)
	for _, name := range order {
		for line := range strings.SplitSeq(blocks[name], "\n") {
			if !strings.HasPrefix(line, "    if:") {
				continue
			}
			if name == "gate" && strings.Contains(line, "always()") {
				continue
			}
			t.Errorf("the %s job carries a job level condition %q; a skipped job fails the gate, "+
				"so a feature must be handled inside the job", name, strings.TrimSpace(line))
		}
	}
}

// The toolchain comes from go.mod. A version written into the workflow is a
// second declaration of the same fact, and the two drift.
func TestGoVersionComesFromTheModule(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	if !strings.Contains(workflow, "go-version-file: go.mod") {
		t.Error("the workflow must read the Go version from go.mod")
	}
	literal := regexp.MustCompile(`go-version:\s*['"]?[0-9]`)
	if literal.MatchString(workflow) {
		t.Error("the workflow pins a Go version by hand; read it from go.mod instead")
	}
	setupCount := strings.Count(workflow, "actions/setup-go@")
	if got := strings.Count(workflow, "go-version-file: go.mod"); got != setupCount {
		t.Errorf("%d Go setups but %d go.mod references; every setup reads the module file",
			setupCount, got)
	}
	if got := strings.Count(workflow, "cache-dependency-path: go.sum"); got != setupCount {
		t.Errorf("%d Go setups but %d cache keys; the module cache is keyed on the lockfile", setupCount, got)
	}
}

// A branch run cancels its predecessor. A default branch run does not, because
// the release path reads its result.
func TestConcurrencyKeepsDefaultBranchRuns(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	if !strings.Contains(workflow, "concurrency:") {
		t.Fatal("the workflow declares no concurrency group")
	}
	if !strings.Contains(workflow, "cancel-in-progress: ${{ github.ref_name != github.event.repository.default_branch }}") {
		t.Error("cancellation must be off on the default branch and on elsewhere")
	}
}

// The frontend dependency cache is keyed on the lockfile digest, so a cache
// hit and a cache miss install the same tree.
func TestFrontendCacheIsKeyedOnTheLockfile(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	_, blocks := jobs(t, workflow)
	frontend := blocks["frontend"]
	if !strings.Contains(frontend, "hashFiles('frontend/bun.lock'") {
		t.Error("the frontend cache key must hash the lockfile")
	}
	if !strings.Contains(frontend, "make frontend-install") {
		t.Error("the frontend job must install from the frozen lockfile")
	}
}

// Ownership coverage is a property of a file, so it runs on every pull request
// rather than only where an administrative token exists. The same job holds
// the drift check and the suppression rules: what a service declares about
// itself, which no Go gate reads.
func TestChecksCoverWhatTheServiceDeclares(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	_, blocks := jobs(t, workflow)
	checks, ok := blocks["checks"]
	if !ok {
		t.Fatal("the workflow declares no checks job")
	}
	for _, want := range []string{"make template-check", "make settings-verify", "suppressions.sh"} {
		if !strings.Contains(checks, want) {
			t.Errorf("the checks job does not run %q", want)
		}
	}
}

// A service runs the shared per-push bar from ci.yml, so a check that bar
// holds and this workflow repeats runs twice on every push and can disagree
// with itself when the two pin different versions of the same tool.
func TestNoCheckRepeatsTheSharedBar(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	shared := []string{
		"make fmt-check", "make lint-modernize", "make spec-check", "make lint-otel",
		"make lint-config", "golangci-lint", "govulncheck", "go vet",
		"make test-hermetic", "make test-tempdir",
	}
	for line := range strings.SplitSeq(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "run: make test" {
			t.Error("verify.yml runs make test, which the shared bar in ci.yml already runs")
		}
		for _, command := range shared {
			if strings.Contains(trimmed, command) {
				t.Errorf("verify.yml runs %q, which the shared bar in ci.yml already runs: %s", command, trimmed)
			}
		}
	}
}

// The version gate is the first job, because every later job assumes the
// generated files this workflow reads.
func TestPrepareGatesTheTemplateVersion(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	order, blocks := jobs(t, workflow)
	if order[0] != "prepare" {
		t.Errorf("the first job is %q, want prepare", order[0])
	}
	if !strings.Contains(workflow, "MIN_TEMPLATE_VERSION:") {
		t.Error("the workflow must declare the minimum template version it accepts")
	}
	if !strings.Contains(blocks["prepare"], "template-version.sh") {
		t.Error("the prepare job must read the consumer declaration and compare its version")
	}
}

// Every script the workflow runs is committed beside it, so a run cannot ask
// for a file the template does not ship.
func TestReferencedScriptsExist(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	referenced := regexp.MustCompile(`\$SCRIPTS/([a-z-]+\.sh)`).FindAllStringSubmatch(workflow, -1)
	if len(referenced) == 0 {
		t.Fatal("the workflow runs no pipeline script")
	}
	for _, m := range referenced {
		path := filepath.Join(repoRoot(t), ".github", "scripts", m[1])
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the workflow runs %s, which is missing: %v", m[1], err)
		}
	}
}

// The code scanning job asserts that the scan produced a result, and a fork run
// analyzes without uploading rather than skipping the job.
func TestCodeScanningAssertsTheScanRan(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "verify.yml")
	_, blocks := jobs(t, workflow)
	codeql, ok := blocks["codeql"]
	if !ok {
		t.Fatal("the workflow declares no code scanning job")
	}
	if !strings.Contains(codeql, "scan-guard.sh") {
		t.Error("the code scanning job must assert that the scan produced a result")
	}
	if !strings.Contains(codeql, "upload: ${{ github.event.pull_request.head.repo.fork != true }}") {
		t.Error("a fork run must analyze without uploading, rather than skip the job")
	}
}

// The caller is the one file a consumer commits, and the job id in it decides
// the name of the required status check.
func TestCallerExample(t *testing.T) {
	caller := readFile(t, "examples", "verify.yml")
	lines := strings.Split(strings.TrimRight(caller, "\n"), "\n")
	if len(lines) >= 20 {
		t.Errorf("the caller is %d lines; the contract promises fewer than twenty", len(lines))
	}
	if !strings.Contains(caller, ".github/workflows/verify.yml@") {
		t.Error("the caller must call the reusable workflow")
	}
	order, _ := jobs(t, caller)
	if len(order) != 1 {
		t.Fatalf("the caller declares %d jobs, want one", len(order))
	}
	// Branch protection requires "<caller job id> / gate". The declared
	// settings and the caller are one decision, so the two files must agree.
	settings := readFile(t, "skeleton", ".github", "settings.yml")
	want := order[0] + " / gate"
	if !strings.Contains(settings, want) {
		t.Errorf("the declared settings do not require %q, so the gate binds nothing", want)
	}
}

// The scheduled settings run reports drift. Applying is a deliberate act, so
// the default mode reverts nothing and the caller dispatches an apply by hand.
func TestSettingsWorkflowReportsByDefault(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "settings.yml")
	if !strings.Contains(workflow, "default: report") {
		t.Error("the settings workflow must default to reporting drift rather than applying it")
	}
	if !strings.Contains(workflow, "-mode=required-check") {
		t.Error("the settings workflow must report a repository whose gate is not required")
	}

	caller := readFile(t, "examples", "settings.yml")
	if !strings.Contains(caller, "schedule:") {
		t.Error("the caller must run on a schedule, or drift is only noticed by accident")
	}
	if !strings.Contains(caller, "mode: ${{ inputs.mode || 'report' }}") {
		t.Error("the scheduled call must report; an apply is dispatched by hand")
	}
}

// The settings tool needs a token with administration rights, which the
// pipeline token does not carry, so the caller passes it as a secret.
func TestSettingsWorkflowTakesAnAdministrativeToken(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "settings.yml")
	if !strings.Contains(workflow, "settings-token:") {
		t.Error("the settings workflow must declare the token it needs")
	}
	if !strings.Contains(workflow, "required: true") {
		t.Error("the token must be required, or the run fails halfway through")
	}
}

// The caller in examples/ is what the generator materializes into a consumer
// repository. Two copies of the same file drift, so they are compared.
func TestGeneratedCallersMatchTheExamples(t *testing.T) {
	for _, name := range []string{"ci.yml", "verify.yml", "ci-gate-bump.yml", "settings.yml"} {
		example := readFile(t, "examples", name)
		generated := readFile(t, "skeleton", ".github", "workflows", name)
		if example != generated {
			t.Errorf("examples/%s and the generated caller differ; a consumer would get "+
				"a pipeline the documentation does not describe", name)
		}
	}
}

// The shared bar sees the root module only. The skeleton as its own module,
// the manifest, the committed reference service, and the adoption proof are
// checked by `make validate`, so the template's own gate runs that target, or
// they are checked only where somebody remembers to run them.
func TestTheTemplateGateRunsValidate(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "template.yml")
	_, blocks := jobs(t, workflow)
	validate, ok := blocks["validate"]
	if !ok {
		t.Fatal("template.yml declares no validate job")
	}
	if !strings.Contains(validate, "run: make validate") {
		t.Error("the validate job does not run make validate")
	}
	if strings.Contains(validate, "if:") {
		t.Error("the validate job is conditional, so a run can skip it")
	}
	makefile := readFile(t, "Makefile")
	target := regexp.MustCompile(`(?m)^validate:(.*)$`).FindStringSubmatch(makefile)
	if target == nil {
		t.Fatal("the Makefile declares no validate target")
	}
	for _, want := range []string{"skeleton-test", "skeleton-lint", "manifest", "example", "adoption"} {
		if !strings.Contains(" "+target[1]+" ", " "+want+" ") {
			t.Errorf("make validate does not run %s", want)
		}
	}
}

// The callers a service commits pin the moving major tag, and deps.yml reads
// the latest GitHub release. A version tag therefore publishes a release and
// then moves the tag the callers pin, or a service's callers resolve nothing
// and its template-version job reads no release.
func TestAVersionTagPublishesAReleaseAndMovesTheMajorTag(t *testing.T) {
	workflow := readFile(t, ".github", "workflows", "template-release.yml")
	if !strings.Contains(workflow, `tags: ["v*.*.*"]`) {
		t.Error("the release workflow must run on a full version tag, and not on the major tag it moves")
	}
	_, blocks := jobs(t, workflow)
	if !strings.Contains(blocks["notes"], "uses: latere-ai/ci/.github/workflows/notes-release.yml@v1") {
		t.Error("a version tag must publish the GitHub release with its CHANGELOG.md section")
	}
	major, ok := blocks["major"]
	if !ok {
		t.Fatal("the release workflow does not move the major tag")
	}
	if needs := needsOf(t, major); len(needs) != 1 || needs[0] != "notes" {
		t.Errorf("the major tag moves before the release is published: needs %v", needs)
	}
	pinned := regexp.MustCompile(`service-template/\.github/workflows/[a-z-]+\.yml@(v[0-9]+)\b`).
		FindStringSubmatch(readFile(t, "examples", "verify.yml"))
	if pinned == nil {
		t.Fatal("the verify caller pins no major tag")
	}
	for _, want := range []string{"git tag -f " + pinned[1] + " ", "refs/tags/" + pinned[1], `grep -Eq '^` + pinned[1] + `\.`} {
		if !strings.Contains(major, want) {
			t.Errorf("the major job does not move %s, the tag the callers pin: no %q", pinned[1], want)
		}
	}
}
