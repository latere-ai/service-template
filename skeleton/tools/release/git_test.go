package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseLogReadsRecords(t *testing.T) {
	out := strings.Join([]string{
		"aaa" + fieldSep + "feat: add" + fieldSep + "body line" + recordSep,
		"bbb" + fieldSep + "fix: repair" + fieldSep + "" + recordSep,
	}, "\n")
	commits := ParseLog(out)
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	if commits[0].SHA != "aaa" || commits[0].Subject != "feat: add" || commits[0].Body != "body line" {
		t.Errorf("first commit = %+v", commits[0])
	}
	if commits[1].Body != "" {
		t.Errorf("second commit body = %q, want empty", commits[1].Body)
	}
}

// A subject holding the separator characters must not split a record.
func TestParseLogSurvivesAwkwardSubjects(t *testing.T) {
	commits := ParseLog("aaa" + fieldSep + "fix: handle a | pipe and a\ttab" + fieldSep + "" + recordSep)
	if len(commits) != 1 || !strings.Contains(commits[0].Subject, "| pipe") {
		t.Fatalf("got %+v", commits)
	}
}

func TestResolveCommit(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git rev-parse HEAD^{commit}", result: ok("abc123\n")}}}
	got, err := ResolveCommit(context.Background(), r, "HEAD")
	if err != nil {
		t.Fatalf("ResolveCommit: %v", err)
	}
	if got != "abc123" {
		t.Errorf("ResolveCommit = %q", got)
	}
}

func TestResolveCommitReportsAnUnknownRef(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git rev-parse", result: fails("unknown revision")}}}
	if _, err := ResolveCommit(context.Background(), r, "nope"); err == nil {
		t.Fatal("ResolveCommit accepted an unknown ref")
	}
}

func TestCommitTimestamp(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git show -s --format=%ct", result: ok("1700000000\n")}}}
	got, err := CommitTimestamp(context.Background(), r, "abc123")
	if err != nil {
		t.Fatalf("CommitTimestamp: %v", err)
	}
	if got != 1700000000 {
		t.Errorf("CommitTimestamp = %d", got)
	}
}

func TestCommitTimestampRejectsNonNumericOutput(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git show", result: ok("yesterday")}}}
	if _, err := CommitTimestamp(context.Background(), r, "abc"); err == nil {
		t.Fatal("CommitTimestamp accepted a non-numeric time")
	}
}

// The version after a candidate is derived from the previous release, so a
// pre-release tag is not a predecessor.
func TestPreviousReleaseSkipsCandidates(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{
		match:  "git tag --list",
		result: ok("v1.0.0\nv1.2.0\nv1.3.0-rc.1\nv0.9.0\nnot-a-version\n"),
	}}}
	got, err := PreviousRelease(context.Background(), r, "abc123")
	if err != nil {
		t.Fatalf("PreviousRelease: %v", err)
	}
	if got != "v1.2.0" {
		t.Errorf("PreviousRelease = %q, want v1.2.0", got)
	}
}

func TestPreviousReleaseWithNoTags(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git tag --list", result: ok("")}}}
	got, err := PreviousRelease(context.Background(), r, "abc123")
	if err != nil || got != "" {
		t.Fatalf("PreviousRelease = %q, %v; want an empty tag and no error", got, err)
	}
}

func TestCommitsSinceUsesTheRange(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{
		match:  "git log",
		result: ok("aaa" + fieldSep + "feat: add" + fieldSep + "" + recordSep),
	}}}
	commits, err := CommitsSince(context.Background(), r, "v1.0.0", "abc123")
	if err != nil {
		t.Fatalf("CommitsSince: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits", len(commits))
	}
	if !r.called("git log --format=") || !strings.Contains(strings.Join(r.calls, " "), "v1.0.0..abc123") {
		t.Errorf("calls = %v, want the range between the tag and the commit", r.calls)
	}
}

func TestCommitsSinceWithNoPreviousTagWalksTheWholeHistory(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git log", result: ok("")}}}
	if _, err := CommitsSince(context.Background(), r, "", "abc123"); err != nil {
		t.Fatalf("CommitsSince: %v", err)
	}
	if strings.Contains(strings.Join(r.calls, " "), "..") {
		t.Errorf("calls = %v, want no range", r.calls)
	}
}

func TestWorkingTreeClean(t *testing.T) {
	clean := &fakeRunner{stubs: []stub{{match: "git status --porcelain", result: ok("")}}}
	if got, err := WorkingTreeClean(context.Background(), clean); err != nil || !got {
		t.Fatalf("WorkingTreeClean = %v, %v; want true", got, err)
	}
	dirty := &fakeRunner{stubs: []stub{{match: "git status --porcelain", result: ok(" M main.go\n")}}}
	if got, _ := WorkingTreeClean(context.Background(), dirty); got {
		t.Error("WorkingTreeClean = true for a modified tree")
	}
}

func TestTagExists(t *testing.T) {
	present := &fakeRunner{stubs: []stub{{match: "git rev-parse --verify", result: ok("abc123")}}}
	if !TagExists(context.Background(), present, "v1.0.0") {
		t.Error("TagExists = false for a tag that resolves")
	}
	absent := &fakeRunner{stubs: []stub{{match: "git rev-parse --verify", result: ok("")}}}
	if TagExists(context.Background(), absent, "v1.0.0") {
		t.Error("TagExists = true for a tag that resolves to nothing")
	}
}

func TestCreateAndPushTag(t *testing.T) {
	r := &fakeRunner{stubs: []stub{
		{match: "git tag -a", result: ok("")},
		{match: "git push origin refs/tags/v1.0.0", result: ok("")},
	}}
	if err := CreateAndPushTag(context.Background(), r, "origin", "v1.0.0", "abc123", "v1.0.0"); err != nil {
		t.Fatalf("CreateAndPushTag: %v", err)
	}
	if !r.called("git push origin refs/tags/v1.0.0") {
		t.Errorf("calls = %v, want the tag pushed by its full ref", r.calls)
	}
}

func TestCreateAndPushTagReportsAFailedPush(t *testing.T) {
	r := &fakeRunner{stubs: []stub{
		{match: "git tag -a", result: ok("")},
		{match: "git push", result: fails("permission denied")},
	}}
	err := CreateAndPushTag(context.Background(), r, "origin", "v1.0.0", "abc123", "v1.0.0")
	if err == nil {
		t.Fatal("CreateAndPushTag reported success for a failed push")
	}
	mustContain(t, err.Error(), "v1.0.0", "the error")
}
