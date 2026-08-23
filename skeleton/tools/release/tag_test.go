package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

const testCommit = "1111111111111111111111111111111111111111"

// releaseRun is the run a pushed tag starts.
func releaseRun(status, conclusion string) VerifyRun {
	return VerifyRun{
		ID: 77, HeadSHA: testCommit, HeadBranch: "main", Status: status,
		Conclusion: conclusion, Event: "push", Path: DefaultReleaseWorkflow,
		URL: "https://example.test/runs/77",
	}
}

// releaseJSON renders a published release.
func releaseJSON(body string, pre bool) string {
	prerelease := "false"
	if pre {
		prerelease = "true"
	}
	return `{"tag_name":"v1.4.3","body":` + quote(body) + `,"prerelease":` + prerelease +
		`,"html_url":"https://example.test/releases/v1.4.3"}`
}

// quote renders a JSON string.
func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `"`, `\"`), "\n", `\n`) + `"`
}

// releaseStubs answers the commands a release issues, with the verify run the
// gate reads.
func releaseStubs(t *testing.T, runs ...VerifyRun) []stub {
	t.Helper()
	return []stub{
		{match: "git status --porcelain", result: ok("")},
		{match: "git rev-parse --verify", result: ok("")},
		{match: "git rev-parse HEAD^{commit}", result: ok(testCommit)},
		{match: "git tag --list", result: ok("v1.4.2\n")},
		{match: "git tag -a", result: ok("")},
		{match: "git push", result: ok("")},
		{match: "git log", result: ok("aaa" + fieldSep + "fix: repair the pool" + fieldSep + "" + recordSep)},
		{match: "gh api repos/owner/name/actions/runs", result: ok(runsJSON(t, runs...))},
	}
}

func releaseOptions(out *strings.Builder) ReleaseOptions {
	return ReleaseOptions{
		Repo: "owner/name",
		Gate: GateOptions{Repo: "owner/name", Branch: "main"},
		Out:  out,
	}
}

// The command is the deterministic half of the gate and runs before the tag
// exists, so a maintainer can still change their mind.
func TestReleaseDryRunProvesTheGateWithoutTagging(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: releaseStubs(t, passingRun(testCommit))}
	o := releaseOptions(&out)
	o.DryRun = true

	if err := Release(context.Background(), r, o); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustContain(t, out.String(), "v1.4.3", "the output")
	mustContain(t, out.String(), "fix: repair the pool", "the output")
	if r.called("git tag -a") || r.called("git push") {
		t.Errorf("a dry run tagged the commit: %v", r.calls)
	}
}

// The gate is checked twice, once before the tag is pushed and once inside the
// pipeline. This is the first check.
func TestReleaseRefusesToTagAnUnverifiedCommit(t *testing.T) {
	failed := passingRun(testCommit)
	failed.Conclusion = "failure"

	var out strings.Builder
	r := &fakeRunner{stubs: releaseStubs(t, failed)}
	err := Release(context.Background(), r, releaseOptions(&out))
	if err == nil {
		t.Fatal("Release tagged a commit with no passing verify run")
	}
	mustContain(t, err.Error(), "refusing to tag", "the failure")
	if r.called("git push") {
		t.Errorf("the tag was pushed anyway: %v", r.calls)
	}
}

func TestReleaseRefusesADirtyTree(t *testing.T) {
	stubs := releaseStubs(t, passingRun(testCommit))
	stubs[0] = stub{match: "git status --porcelain", result: ok(" M main.go")}

	var out strings.Builder
	r := &fakeRunner{stubs: stubs}
	if err := Release(context.Background(), r, releaseOptions(&out)); err == nil {
		t.Fatal("Release tagged from a tree with uncommitted changes")
	}
	if r.called("git push") {
		t.Errorf("the tag was pushed anyway: %v", r.calls)
	}
}

func TestReleaseRefusesAnExistingTag(t *testing.T) {
	stubs := releaseStubs(t, passingRun(testCommit))
	stubs[1] = stub{match: "git rev-parse --verify", result: ok(testCommit)}

	var out strings.Builder
	o := releaseOptions(&out)
	o.Watch = false
	r := &fakeRunner{stubs: stubs}
	if err := Release(context.Background(), r, o); err == nil {
		t.Fatal("Release pushed a tag that already exists")
	}
}

func TestReleasePushesTheTagAtTheVerifiedCommit(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: releaseStubs(t, passingRun(testCommit))}
	o := releaseOptions(&out)
	o.Watch = false

	if err := Release(context.Background(), r, o); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !r.called("git push origin refs/tags/v1.4.3") {
		t.Fatalf("calls = %v, want the derived tag pushed", r.calls)
	}
	if !strings.Contains(strings.Join(r.calls, " "), testCommit) {
		t.Errorf("the tag was not created at the verified commit: %v", r.calls)
	}
}

// clock returns a time source that advances by step on every read.
func clock(start time.Time, step time.Duration) func() time.Time {
	now := start
	return func() time.Time {
		now = now.Add(step)
		return now
	}
}

// watchOptions is a watch with no real waiting.
func watchOptions(out *strings.Builder) ReleaseOptions {
	o := releaseOptions(out)
	o.Sleep = func(time.Duration) {}
	o.Now = clock(time.Unix(0, 0), time.Second)
	o.PollInterval = time.Millisecond
	return o
}

// A pipeline that finished is not the same claim as a version that is
// serving, so the command reads the smoke evidence out of the published
// release before it says live.
func TestWatchReportsLiveOnlyOnTheSmokeEvidence(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/releases/tags/v1.4.3",
			result: ok(releaseJSON("Notes\n\n"+LiveCheckHeading+": production\n", false))},
		{match: "gh api repos/owner/name/actions/runs",
			result: ok(runsJSON(t, releaseRun("completed", "success")))},
	}}

	if err := WatchRelease(context.Background(), r, watchOptions(&out), "v1.4.3", testCommit); err != nil {
		t.Fatalf("WatchRelease: %v", err)
	}
	mustContain(t, out.String(), "release v1.4.3 is live", "the output")
}

func TestWatchRefusesToReportLiveWithoutEvidence(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/releases/tags/v1.4.3",
			result: ok(releaseJSON("Notes with no evidence\n", false))},
		{match: "gh api repos/owner/name/actions/runs",
			result: ok(runsJSON(t, releaseRun("completed", "success")))},
	}}

	err := WatchRelease(context.Background(), r, watchOptions(&out), "v1.4.3", testCommit)
	if err == nil {
		t.Fatal("the command reported a version live with no live check evidence")
	}
	mustContain(t, err.Error(), "no live check evidence", "the failure")
}

func TestWatchReportsAPreRelease(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/releases/tags/v1.4.3",
			result: ok(releaseJSON(LiveCheckHeading+": preproduction\n", true))},
		{match: "gh api repos/owner/name/actions/runs",
			result: ok(runsJSON(t, releaseRun("completed", "success")))},
	}}
	if err := WatchRelease(context.Background(), r, watchOptions(&out), "v1.4.3", testCommit); err != nil {
		t.Fatalf("WatchRelease: %v", err)
	}
	mustContain(t, out.String(), "pre-release v1.4.3 is live", "the output")
}

func TestWatchReportsAFailedPipeline(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/actions/runs",
			result: ok(runsJSON(t, releaseRun("completed", "failure")))},
	}}
	err := WatchRelease(context.Background(), r, watchOptions(&out), "v1.4.3", testCommit)
	if err == nil {
		t.Fatal("WatchRelease reported success for a failed pipeline")
	}
	mustContain(t, err.Error(), "ended failure", "the failure")
}

func TestWatchReportsAMissingRelease(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/releases/tags/v1.4.3", result: fails("not found")},
		{match: "gh api repos/owner/name/actions/runs",
			result: ok(runsJSON(t, releaseRun("completed", "success")))},
	}}
	err := WatchRelease(context.Background(), r, watchOptions(&out), "v1.4.3", testCommit)
	if err == nil {
		t.Fatal("WatchRelease reported success with no published release")
	}
	mustContain(t, err.Error(), "no release was published", "the failure")
}

func TestWatchGivesUpAfterTheTimeout(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/actions/runs",
			result: ok(runsJSON(t, releaseRun("in_progress", "")))},
	}}
	o := watchOptions(&out)
	o.WatchTimeout = 3 * time.Second

	err := WatchRelease(context.Background(), r, o, "v1.4.3", testCommit)
	if err == nil {
		t.Fatal("WatchRelease waited for a pipeline that never finished")
	}
	mustContain(t, err.Error(), "did not finish", "the failure")
	mustContain(t, out.String(), "release pipeline in_progress", "the output")
}

func TestWatchWaitsForThePipelineToStart(t *testing.T) {
	var out strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "gh api repos/owner/name/actions/runs", result: ok(runsJSON(t))},
	}}
	o := watchOptions(&out)
	o.WatchTimeout = 2 * time.Second

	if err := WatchRelease(context.Background(), r, o, "v1.4.3", testCommit); err == nil {
		t.Fatal("WatchRelease returned before a pipeline existed")
	}
	mustContain(t, out.String(), "waiting for the release pipeline", "the output")
}
