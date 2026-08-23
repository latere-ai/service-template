package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchRejectsAnUnknownOrEmptyCommand(t *testing.T) {
	var out strings.Builder
	if err := dispatch(context.Background(), &fakeRunner{}, nil, envOf(nil), &out); err == nil {
		t.Error("dispatch accepted no subcommand")
	}
	if err := dispatch(context.Background(), &fakeRunner{}, []string{"deploy"}, envOf(nil), &out); err == nil {
		t.Error("dispatch accepted an unknown subcommand")
	}
}

func TestDispatchReportsBadFlags(t *testing.T) {
	var out strings.Builder
	for _, name := range []string{"version", "gate", "release", "preflight", "rollout", "evidence", "check", "pins", "secrets"} {
		if err := dispatch(context.Background(), &fakeRunner{}, []string{name, "-nonsense"}, envOf(nil), &out); err == nil {
			t.Errorf("%s accepted an unknown flag", name)
		}
	}
}

// The derived version is published as a job output, so the steps that follow
// use the same number the summary printed.
func TestVersionSubcommandWritesJobOutputs(t *testing.T) {
	dir := t.TempDir()
	outputFile := filepath.Join(dir, "output.txt")
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "git rev-parse HEAD^{commit}", result: ok(testCommit)},
		{match: "git tag --list", result: ok("v1.4.2")},
		{match: "git log", result: ok("aaa" + fieldSep + "feat: add" + fieldSep + "" + recordSep)},
	}}

	err := dispatch(context.Background(), r, []string{"version"},
		envOf(map[string]string{EnvJobOutput: outputFile}), &out)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	mustContain(t, out.String(), "v1.5.0", "the output")

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read the job output: %v", err)
	}
	for _, want := range []string{"version=v1.5.0", "bump=minor", "previous=v1.4.2", "commit=" + testCommit} {
		mustContain(t, string(data), want, "the job output")
	}
}

func TestGateSubcommandPublishesTheProvingRun(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "output.txt")
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok(runsJSON(t, passingRun(testCommit)))}}}

	err := dispatch(context.Background(), r,
		[]string{"gate", "-repo", "owner/name", "-commit", testCommit, "-branch", "main"},
		envOf(map[string]string{EnvJobOutput: outputFile}), &out)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	data, _ := os.ReadFile(outputFile)
	mustContain(t, string(data), "verify_run=https://example.test/runs/42", "the job output")
	mustContain(t, string(data), "verify_run_id=42", "the job output")
}

func TestGateSubcommandFailsWithoutAProvingRun(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok(runsJSON(t))}}}
	err := dispatch(context.Background(), r,
		[]string{"gate", "-repo", "owner/name", "-commit", testCommit}, envOf(nil), &out)
	if err == nil {
		t.Fatal("the gate subcommand passed with no run")
	}
}

func TestEvidenceSubcommandWritesTheBlock(t *testing.T) {
	dir := t.TempDir()
	e := complete()
	live := e.LiveCheck
	e.LiveCheck = ""
	body, err := marshalEvidence(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	in := writeFile(t, dir, "evidence.json", body)
	liveFile := writeFile(t, dir, "smoke.md", live)
	outFile := filepath.Join(dir, "notes.md")

	var out strings.Builder
	err = dispatch(context.Background(), &fakeRunner{},
		[]string{"evidence", "-in", in, "-live", liveFile, "-out", outFile}, envOf(nil), &out)
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read the notes: %v", err)
	}
	mustContain(t, string(data), "## Release evidence", "the notes")
	mustContain(t, string(data), "### Live check", "the notes")
}

// A missing live check is a gap, and a gap fails the release.
func TestEvidenceSubcommandFailsOnAnIncompleteBlock(t *testing.T) {
	dir := t.TempDir()
	e := complete()
	e.LiveCheck = ""
	body, err := marshalEvidence(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	in := writeFile(t, dir, "evidence.json", body)

	var out strings.Builder
	err = dispatch(context.Background(), &fakeRunner{}, []string{"evidence", "-in", in}, envOf(nil), &out)
	if err == nil {
		t.Fatal("the evidence subcommand accepted a block with no live check")
	}
	mustContain(t, err.Error(), "live check is empty", "the failure")
}

func TestEvidenceSubcommandNeedsItsInput(t *testing.T) {
	var out strings.Builder
	if err := dispatch(context.Background(), &fakeRunner{}, []string{"evidence"}, envOf(nil), &out); err == nil {
		t.Error("the evidence subcommand ran with no input")
	}
	missing := filepath.Join(t.TempDir(), "absent.json")
	if err := dispatch(context.Background(), &fakeRunner{}, []string{"evidence", "-in", missing}, envOf(nil), &out); err == nil {
		t.Error("the evidence subcommand ran with an input that does not exist")
	}
}

func TestCheckSubcommandReadsTheShippedTree(t *testing.T) {
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{},
		[]string{"check", "-root", repoRoot}, envOf(nil), &out)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	mustContain(t, out.String(), "hold the contract", "the output")
}

func TestPinsSubcommandChecksADirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "release.yml", "steps:\n  - uses: actions/checkout@v4\n")
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{}, []string{"pins", "-dir", dir}, envOf(nil), &out)
	if err == nil {
		t.Fatal("the pins subcommand accepted a tag reference")
	}
	mustContain(t, err.Error(), "unpinned action reference", "the failure")
}

func TestSecretsSubcommandListsTheDeclaration(t *testing.T) {
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{},
		[]string{"secrets", "-file", "../../" + SecretsFile}, envOf(nil), &out)
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	mustContain(t, out.String(), "registry", "the output")
	mustContain(t, out.String(), "federated", "the output")
}

func TestRolloutSubcommandReportsTheResult(t *testing.T) {
	root := deployTree(t)
	outputFile := filepath.Join(t.TempDir(), "output.txt")
	summaryFile := filepath.Join(t.TempDir(), "summary.md")
	var out strings.Builder
	r := healthyRunner(filepath.Join(root, "production"))

	err := dispatch(context.Background(), r, []string{
		"rollout", "-root", root, "-target", "production", "-service", "service",
		"-image", "ghcr.io/owner/service",
		"-digest", "sha256:" + strings.Repeat("1", 64),
	}, envOf(map[string]string{EnvJobOutput: outputFile, EnvStepSummary: summaryFile}), &out)
	if err != nil {
		t.Fatalf("rollout: %v", err)
	}

	outputs, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read the job output: %v", err)
	}
	for _, want := range []string{"namespace=service-production", "replicas=2", "completed=", "image=ghcr.io/owner/service@sha256:"} {
		mustContain(t, string(outputs), want, "the job output")
	}
	summary, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	mustContain(t, string(summary), "2 of 2 replicas ready", "the summary")
}

func TestRolloutSubcommandDerivesTheServiceName(t *testing.T) {
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{},
		[]string{"rollout", "-root", t.TempDir(), "-target", "production"}, envOf(nil), &out)
	if err == nil {
		t.Fatal("the rollout subcommand ran against a tree with no target")
	}
}

func TestPreflightSubcommandUsesTheRunEnvironment(t *testing.T) {
	summaryFile := filepath.Join(t.TempDir(), "summary.md")
	values := identityEnv()
	values[EnvStepSummary] = summaryFile

	var out strings.Builder
	err := dispatch(context.Background(), runnerFor("yes", "no"), []string{
		"preflight", "-namespace", "service-production", "-secrets", "../../" + SecretsFile,
	}, envOf(values), &out)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	summary, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatalf("read the summary: %v", err)
	}
	mustContain(t, string(summary), "### Preflight", "the summary")
}

// A value with a newline is written in the delimited form the runner expects,
// so a multi-line output does not corrupt the file.
func TestWriteOutputsHandlesMultiLineValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	err := writeOutputs(envOf(map[string]string{EnvJobOutput: path}), map[string]string{
		"single": "one",
		"multi":  "one\ntwo",
	})
	if err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}
	data, _ := os.ReadFile(path)
	mustContain(t, string(data), "single=one", "the job output")
	mustContain(t, string(data), "multi<<RELEASE_EOF\none\ntwo\nRELEASE_EOF", "the job output")
	if strings.Index(string(data), "multi") > strings.Index(string(data), "single") {
		t.Errorf("the keys are not in a stable order:\n%s", data)
	}
}

func TestWriteOutputsIsANoOpOutsideARun(t *testing.T) {
	if err := writeOutputs(envOf(nil), map[string]string{"a": "b"}); err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}
}

func TestSummaryWriterFallsBackToStandardOutput(t *testing.T) {
	var out strings.Builder
	w, done, err := summaryWriter(envOf(nil), &out)
	if err != nil {
		t.Fatalf("summaryWriter: %v", err)
	}
	defer done()
	if w != &out {
		t.Error("summaryWriter did not fall back to standard output")
	}
}

func TestJoinRoot(t *testing.T) {
	cases := map[[2]string]string{
		{".", "Dockerfile"}:  "Dockerfile",
		{"", "Dockerfile"}:   "Dockerfile",
		{"../..", "deploy"}:  "../../deploy",
		{"/repo/", "deploy"}: "/repo/deploy",
	}
	for in, want := range cases {
		if got := joinRoot(in[0], in[1]); got != want {
			t.Errorf("joinRoot(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestVersionSubcommandReportsAFailingRepository(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{{match: "git rev-parse", result: fails("not a repository")}}}
	if err := dispatch(context.Background(), r, []string{"version"}, envOf(nil), &out); err == nil {
		t.Fatal("the version subcommand ran outside a repository")
	}
}

func TestVersionSubcommandReportsAnEmptyRange(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "git rev-parse", result: ok(testCommit)},
		{match: "git tag --list", result: ok("v1.0.0")},
		{match: "git log", result: ok("")},
	}}
	if err := dispatch(context.Background(), r, []string{"version"}, envOf(nil), &out); err == nil {
		t.Fatal("the version subcommand derived a version with nothing to release")
	}
}

func TestPinsSubcommandAcceptsAPinnedDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "verify.yml", "steps:\n  - uses: actions/checkout@"+strings.Repeat("a", 40)+"\n")
	var out strings.Builder
	if err := dispatch(context.Background(), &fakeRunner{}, []string{"pins", "-dir", dir}, envOf(nil), &out); err != nil {
		t.Fatalf("pins: %v", err)
	}
	mustContain(t, out.String(), "pinned by digest", "the output")
}

func TestEvidenceSubcommandWritesToStandardOutput(t *testing.T) {
	body, err := marshalEvidence(complete())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	in := writeFile(t, t.TempDir(), "evidence.json", body)
	var out strings.Builder
	if err := dispatch(context.Background(), &fakeRunner{}, []string{"evidence", "-in", in}, envOf(nil), &out); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	mustContain(t, out.String(), "## Release evidence", "the output")
}

func TestNamespaceSubcommandReadsTheOverlay(t *testing.T) {
	root := deployTree(t)
	outputFile := filepath.Join(t.TempDir(), "output.txt")
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{},
		[]string{"namespace", "-root", root, "-target", "production"},
		envOf(map[string]string{EnvJobOutput: outputFile}), &out)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if strings.TrimSpace(out.String()) != "service-production" {
		t.Errorf("namespace = %q", out.String())
	}
	data, _ := os.ReadFile(outputFile)
	mustContain(t, string(data), "namespace=service-production", "the job output")
}

func TestNamespaceSubcommandRefusesTheBootstrapDirectory(t *testing.T) {
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{},
		[]string{"namespace", "-root", deployTree(t), "-target", BootstrapDir}, envOf(nil), &out)
	if err == nil {
		t.Fatal("the namespace subcommand read the bootstrap directory")
	}
}

func TestNamespaceSubcommandReportsAnOverlayWithoutOne(t *testing.T) {
	root := deployTree(t)
	writeFile(t, root, "production/kustomization.yaml", "resources:\n  - ../base\n")
	var out strings.Builder
	err := dispatch(context.Background(), &fakeRunner{},
		[]string{"namespace", "-root", root, "-target", "production"}, envOf(nil), &out)
	if err == nil {
		t.Fatal("the namespace subcommand accepted an overlay with no namespace")
	}
}
