package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// VerifyRun is one workflow run as the API reports it.
type VerifyRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	URL        string `json:"html_url"`
	CreatedAt  string `json:"created_at"`
}

// runsResponse is the shape of the workflow runs endpoint.
type runsResponse struct {
	Runs []VerifyRun `json:"workflow_runs"`
}

// GateOptions states which run counts as proof.
type GateOptions struct {
	// Repo is owner/name.
	Repo string
	// Workflow is the workflow file path, for example
	// .github/workflows/verify.yml.
	Workflow string
	// Commit is the exact commit the tag points at. Not the branch tip, and
	// not a later commit.
	Commit string
	// Branch limits the proof to runs on one branch. Empty accepts any.
	Branch string
	// Event limits the proof to one trigger. A pull request run tests the
	// merge result rather than the commit as it landed, so accepting one
	// would let an unverified commit through the gate.
	Event string
}

// DefaultVerifyWorkflow is the workflow whose run the gate reads.
const DefaultVerifyWorkflow = ".github/workflows/verify.yml"

// DefaultGateEvent is the trigger a proving run must have come from.
const DefaultGateEvent = "push"

// FindVerifyRun returns the passing verify run for the exact commit, or an
// error naming what it did find. A release built from an unverified commit is
// the failure this gate exists to prevent, and it is easy to hit when a tag is
// pushed while the verify run is still going.
func FindVerifyRun(ctx context.Context, r Runner, o GateOptions) (VerifyRun, error) {
	if o.Commit == "" || o.Repo == "" {
		return VerifyRun{}, fmt.Errorf("the gate needs a repository and a commit")
	}
	path := fmt.Sprintf("repos/%s/actions/runs?head_sha=%s&per_page=100", o.Repo, o.Commit)
	out, err := output(ctx, r, Command{Name: "gh", Args: []string{"api", path}})
	if err != nil {
		return VerifyRun{}, fmt.Errorf("read workflow runs for %s: %w", short(o.Commit), err)
	}

	var resp runsResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return VerifyRun{}, fmt.Errorf("parse workflow runs for %s: %w", short(o.Commit), err)
	}

	var considered []string
	for _, run := range resp.Runs {
		if o.Workflow != "" && run.Path != o.Workflow {
			continue
		}
		considered = append(considered, describeRun(run))
		if !runProves(run, o) {
			continue
		}
		return run, nil
	}
	return VerifyRun{}, gateFailure(o, considered)
}

// runProves states the four conditions together. Each one alone is satisfiable
// by a run that did not verify this commit as it will ship.
func runProves(run VerifyRun, o GateOptions) bool {
	switch {
	case !strings.EqualFold(run.HeadSHA, o.Commit):
		return false
	case run.Status != "completed" || run.Conclusion != "success":
		return false
	case o.Event != "" && run.Event != o.Event:
		return false
	case o.Branch != "" && run.HeadBranch != o.Branch:
		return false
	}
	return true
}

// describeRun renders one candidate for the failure message.
func describeRun(run VerifyRun) string {
	return fmt.Sprintf("run %d: status=%s conclusion=%s event=%s branch=%s (%s)",
		run.ID, run.Status, run.Conclusion, run.Event, run.HeadBranch, run.URL)
}

// gateFailure explains what was required and what was present, because the
// remedy differs: wait for a run, re-run a failed one, or tag a commit that
// actually landed on the default branch.
func gateFailure(o GateOptions, considered []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no passing %s run for commit %s", o.Workflow, o.Commit)
	if o.Event != "" {
		fmt.Fprintf(&b, " triggered by %s", o.Event)
	}
	if o.Branch != "" {
		fmt.Fprintf(&b, " on %s", o.Branch)
	}
	if len(considered) == 0 {
		b.WriteString("\nno run of that workflow exists for this commit")
		return fmt.Errorf("%s", b.String())
	}
	b.WriteString("\nruns found for this commit:")
	for _, c := range considered {
		fmt.Fprintf(&b, "\n  %s", c)
	}
	return fmt.Errorf("%s", b.String())
}
