package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// DefaultReleaseWorkflow is the workflow a pushed tag starts.
const DefaultReleaseWorkflow = ".github/workflows/release.yml"

// LiveCheckHeading is the smoke evidence heading. The release command treats
// the released version as live only once this heading is in the published
// release body, because that is the point at which a live assertion has
// actually been made.
const LiveCheckHeading = "### Live check"

// Watch defaults. The interval is long enough not to consume the API budget
// and short enough that a maintainer watching the output sees movement.
const (
	DefaultPollInterval = 20 * time.Second
	DefaultWatchTimeout = 45 * time.Minute
)

// ReleaseOptions is one maintainer-run release.
type ReleaseOptions struct {
	Repo   string
	Ref    string
	Remote string
	Gate   GateOptions
	// ReleaseWorkflow is the workflow the pushed tag starts.
	ReleaseWorkflow string
	// DryRun proves the gate and prints the derived version without tagging.
	DryRun bool
	// Watch follows the pipeline the tag started.
	Watch        bool
	PollInterval time.Duration
	WatchTimeout time.Duration

	Out   io.Writer
	Sleep func(time.Duration)
	Now   func() time.Time
}

// releaseBody is the part of a published release the command reads.
type releaseBody struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	PreRelease bool   `json:"prerelease"`
	URL        string `json:"html_url"`
}

// Release is the deterministic half of the tag gate. It runs before the tag
// exists, so the same condition the pipeline enforces is also the condition a
// maintainer sees while they can still change their mind.
func Release(ctx context.Context, r Runner, o ReleaseOptions) error {
	o = withReleaseDefaults(o)

	clean, err := WorkingTreeClean(ctx, r)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("the working tree has uncommitted changes; a tag must point at a commit that describes what was tested")
	}

	commit, err := ResolveCommit(ctx, r, o.Ref)
	if err != nil {
		return err
	}
	o.Gate.Commit = commit
	if o.Gate.Repo == "" {
		o.Gate.Repo = o.Repo
	}

	run, err := FindVerifyRun(ctx, r, o.Gate)
	if err != nil {
		return fmt.Errorf("refusing to tag %s: %w", short(commit), err)
	}

	previous, err := PreviousRelease(ctx, r, commit)
	if err != nil {
		return err
	}
	commits, err := CommitsSince(ctx, r, previous, commit)
	if err != nil {
		return err
	}
	next, bump, err := NextVersion(previous, commits)
	if err != nil {
		return err
	}

	printf(o.Out, "commit    %s\n", commit)
	printf(o.Out, "verified  %s\n", run.URL)
	printf(o.Out, "previous  %s\n", describePrevious(previous))
	printf(o.Out, "next      %s (%s)\n\n", next, bump)
	printf(o.Out, "%s", ChangeSummary(previous, commits))

	tag := next.String()
	if o.DryRun {
		printf(o.Out, "\ndry run: %s was not pushed\n", tag)
		return nil
	}
	if TagExists(ctx, r, tag) {
		return fmt.Errorf("tag %s already exists", tag)
	}
	message := fmt.Sprintf("%s\n\nverified by %s", tag, run.URL)
	if err := CreateAndPushTag(ctx, r, o.Remote, tag, commit, message); err != nil {
		return err
	}
	printf(o.Out, "\npushed %s at %s\n", tag, short(commit))

	if !o.Watch {
		return nil
	}
	return WatchRelease(ctx, r, o, tag, commit)
}

// withReleaseDefaults fills the values a caller may leave unset.
func withReleaseDefaults(o ReleaseOptions) ReleaseOptions {
	if o.Ref == "" {
		o.Ref = "HEAD"
	}
	if o.Remote == "" {
		o.Remote = "origin"
	}
	if o.ReleaseWorkflow == "" {
		o.ReleaseWorkflow = DefaultReleaseWorkflow
	}
	if o.Gate.Workflow == "" {
		o.Gate.Workflow = DefaultVerifyWorkflow
	}
	if o.Gate.Event == "" {
		o.Gate.Event = DefaultGateEvent
	}
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultPollInterval
	}
	if o.WatchTimeout <= 0 {
		o.WatchTimeout = DefaultWatchTimeout
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	return o
}

// WatchRelease follows the pipeline the tag started and reports the version as
// live only once the published release carries the smoke evidence. A pipeline
// that finished is not the same claim as a version that is serving.
func WatchRelease(ctx context.Context, r Runner, o ReleaseOptions, tag, commit string) error {
	o = withReleaseDefaults(o)
	deadline := o.Now().Add(o.WatchTimeout)
	var last string
	for {
		run, found, err := findRun(ctx, r, o.Repo, o.ReleaseWorkflow, commit)
		if err != nil {
			return err
		}
		switch {
		case !found:
			if last != "queued" {
				printf(o.Out, "waiting for the release pipeline to start\n")
				last = "queued"
			}
		case run.Status != "completed":
			if last != run.Status {
				printf(o.Out, "release pipeline %s: %s\n", run.Status, run.URL)
				last = run.Status
			}
		case run.Conclusion != "success":
			return fmt.Errorf("the release pipeline for %s ended %s: %s", tag, run.Conclusion, run.URL)
		default:
			return confirmLive(ctx, r, o, tag, run)
		}
		if !o.Now().Before(deadline) {
			return fmt.Errorf("the release pipeline for %s did not finish within %s", tag, o.WatchTimeout)
		}
		o.Sleep(o.PollInterval)
	}
}

// confirmLive reads the published release and reports the version as live only
// when the smoke evidence is in it.
func confirmLive(ctx context.Context, r Runner, o ReleaseOptions, tag string, run VerifyRun) error {
	out, err := output(ctx, r, Command{Name: "gh", Args: []string{
		"api", fmt.Sprintf("repos/%s/releases/tags/%s", o.Repo, tag),
	}})
	if err != nil {
		return fmt.Errorf("the release pipeline for %s succeeded but no release was published: %w", tag, err)
	}
	var body releaseBody
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return fmt.Errorf("parse the published release for %s: %w", tag, err)
	}
	if !strings.Contains(body.Body, LiveCheckHeading) {
		return fmt.Errorf("the release for %s carries no live check evidence, so the version cannot be reported as live: %s",
			tag, body.URL)
	}
	kind := "release"
	if body.PreRelease {
		kind = "pre-release"
	}
	printf(o.Out, "%s %s is live, proved by the live check in %s (pipeline %s)\n", kind, tag, body.URL, run.URL)
	return nil
}

// findRun returns the most recent run of a workflow for a commit.
func findRun(ctx context.Context, r Runner, repo, workflow, commit string) (VerifyRun, bool, error) {
	path := fmt.Sprintf("repos/%s/actions/runs?head_sha=%s&per_page=100", repo, commit)
	out, err := output(ctx, r, Command{Name: "gh", Args: []string{"api", path}})
	if err != nil {
		return VerifyRun{}, false, fmt.Errorf("read workflow runs for %s: %w", short(commit), err)
	}
	var resp runsResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return VerifyRun{}, false, fmt.Errorf("parse workflow runs for %s: %w", short(commit), err)
	}
	for _, run := range resp.Runs {
		if run.Path == workflow {
			return run, true, nil
		}
	}
	return VerifyRun{}, false, nil
}
