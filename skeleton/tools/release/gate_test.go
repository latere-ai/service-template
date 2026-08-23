package main

import (
	"context"
	"encoding/json"
	"testing"
)

// runsJSON renders the API answer the gate reads.
func runsJSON(t *testing.T, runs ...VerifyRun) string {
	t.Helper()
	data, err := json.Marshal(runsResponse{Runs: runs})
	if err != nil {
		t.Fatalf("marshal runs: %v", err)
	}
	return string(data)
}

// passingRun is the run that proves a commit.
func passingRun(commit string) VerifyRun {
	return VerifyRun{
		ID: 42, HeadSHA: commit, HeadBranch: "main", Status: "completed",
		Conclusion: "success", Event: "push", Path: DefaultVerifyWorkflow,
		URL: "https://example.test/runs/42",
	}
}

func gateOptions(commit string) GateOptions {
	return GateOptions{
		Repo: "owner/name", Workflow: DefaultVerifyWorkflow,
		Commit: commit, Branch: "main", Event: DefaultGateEvent,
	}
}

func TestFindVerifyRunAcceptsAPassingRun(t *testing.T) {
	commit := "1111111111111111111111111111111111111111"
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok(runsJSON(t, passingRun(commit)))}}}
	run, err := FindVerifyRun(context.Background(), r, gateOptions(commit))
	if err != nil {
		t.Fatalf("FindVerifyRun: %v", err)
	}
	if run.ID != 42 {
		t.Errorf("run = %+v", run)
	}
}

// The gate exists to stop a release built from an unverified commit, so every
// one of these near misses must fail it.
func TestFindVerifyRunRejectsRunsThatDoNotProveTheCommit(t *testing.T) {
	commit := "1111111111111111111111111111111111111111"
	other := "2222222222222222222222222222222222222222"

	cases := map[string]VerifyRun{
		"still running":  {HeadSHA: commit, Status: "in_progress", Conclusion: "", Event: "push", HeadBranch: "main", Path: DefaultVerifyWorkflow},
		"failed":         {HeadSHA: commit, Status: "completed", Conclusion: "failure", Event: "push", HeadBranch: "main", Path: DefaultVerifyWorkflow},
		"cancelled":      {HeadSHA: commit, Status: "completed", Conclusion: "cancelled", Event: "push", HeadBranch: "main", Path: DefaultVerifyWorkflow},
		"another commit": {HeadSHA: other, Status: "completed", Conclusion: "success", Event: "push", HeadBranch: "main", Path: DefaultVerifyWorkflow},
		"another branch": {HeadSHA: commit, Status: "completed", Conclusion: "success", Event: "push", HeadBranch: "topic", Path: DefaultVerifyWorkflow},
		// A pull request run verifies the merge result rather than the commit
		// as it landed, so it is not proof for a tag.
		"pull request": {HeadSHA: commit, Status: "completed", Conclusion: "success", Event: "pull_request", HeadBranch: "main", Path: DefaultVerifyWorkflow},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok(runsJSON(t, run))}}}
			if _, err := FindVerifyRun(context.Background(), r, gateOptions(commit)); err == nil {
				t.Fatalf("the gate accepted a %s run", name)
			}
		})
	}
}

func TestFindVerifyRunIgnoresOtherWorkflows(t *testing.T) {
	commit := "1111111111111111111111111111111111111111"
	other := passingRun(commit)
	other.Path = ".github/workflows/codeql.yml"
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok(runsJSON(t, other))}}}

	_, err := FindVerifyRun(context.Background(), r, gateOptions(commit))
	if err == nil {
		t.Fatal("a run of another workflow proved the commit")
	}
	mustContain(t, err.Error(), "no run of that workflow exists", "the failure")
}

func TestFindVerifyRunListsWhatItFound(t *testing.T) {
	commit := "1111111111111111111111111111111111111111"
	failed := passingRun(commit)
	failed.Conclusion = "failure"
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok(runsJSON(t, failed))}}}

	_, err := FindVerifyRun(context.Background(), r, gateOptions(commit))
	if err == nil {
		t.Fatal("the gate accepted a failed run")
	}
	mustContain(t, err.Error(), "conclusion=failure", "the failure")
	mustContain(t, err.Error(), "https://example.test/runs/42", "the failure")
}

func TestFindVerifyRunNeedsARepositoryAndACommit(t *testing.T) {
	r := &fakeRunner{}
	if _, err := FindVerifyRun(context.Background(), r, GateOptions{Repo: "owner/name"}); err == nil {
		t.Fatal("the gate ran without a commit")
	}
	if len(r.calls) != 0 {
		t.Errorf("calls = %v, want no API call", r.calls)
	}
}

func TestFindVerifyRunReportsAnApiFailure(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: fails("bad credentials")}}}
	if _, err := FindVerifyRun(context.Background(), r, gateOptions("abc")); err == nil {
		t.Fatal("the gate passed while the API was unreachable")
	}
}

func TestFindVerifyRunReportsUnreadableOutput(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "gh api", result: ok("not json")}}}
	if _, err := FindVerifyRun(context.Background(), r, gateOptions("abc")); err == nil {
		t.Fatal("the gate passed on output it could not parse")
	}
}
