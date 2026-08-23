package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Field and record separators for the log format. They are control characters
// rather than a punctuation choice, so a commit message can contain anything
// without splitting a record.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// logFormat emits one record per commit: hash, subject, body.
const logFormat = "%H" + fieldSep + "%s" + fieldSep + "%b" + recordSep

// git builds a git command.
func git(args ...string) Command { return Command{Name: "git", Args: args} }

// ResolveCommit turns a ref into the full commit it names. Every later step
// works from this value, because "the branch tip" and "the commit the tag
// points at" diverge the moment someone pushes.
func ResolveCommit(ctx context.Context, r Runner, ref string) (string, error) {
	sha, err := output(ctx, r, git("rev-parse", ref+"^{commit}"))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return sha, nil
}

// CommitTimestamp is the commit's author date as a Unix time. The image build
// uses it as its fixed timestamp, which is what makes the file times in the
// image a function of the commit.
func CommitTimestamp(ctx context.Context, r Runner, sha string) (int64, error) {
	out, err := output(ctx, r, git("show", "-s", "--format=%ct", sha))
	if err != nil {
		return 0, fmt.Errorf("read commit time of %s: %w", short(sha), err)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse commit time %q: %w", out, err)
	}
	return ts, nil
}

// PreviousRelease is the highest release tag reachable from sha. Pre-release
// tags are skipped, because the version after v1.3.0-rc.2 is derived from
// v1.2.0 and not from the candidate.
func PreviousRelease(ctx context.Context, r Runner, sha string) (string, error) {
	out, err := output(ctx, r, git("tag", "--list", "v*", "--merged", sha))
	if err != nil {
		return "", fmt.Errorf("list tags reachable from %s: %w", short(sha), err)
	}
	var versions []Version
	tags := map[string]string{}
	for line := range strings.FieldsSeq(out) {
		v, err := ParseVersion(line)
		if err != nil || v.IsPreRelease() {
			continue
		}
		versions = append(versions, v)
		tags[v.String()] = line
	}
	if len(versions) == 0 {
		return "", nil
	}
	sort.Slice(versions, func(i, j int) bool { return less(versions[i], versions[j]) })
	return tags[versions[len(versions)-1].String()], nil
}

// less orders two release versions.
func less(a, b Version) bool {
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	if a.Minor != b.Minor {
		return a.Minor < b.Minor
	}
	return a.Patch < b.Patch
}

// CommitsSince lists the commits a release would carry. An empty previous tag
// means the whole history leading to sha.
func CommitsSince(ctx context.Context, r Runner, previous, sha string) ([]Commit, error) {
	rev := sha
	if strings.TrimSpace(previous) != "" {
		rev = previous + ".." + sha
	}
	out, err := output(ctx, r, git("log", "--format="+logFormat, "--no-merges", rev))
	if err != nil {
		return nil, fmt.Errorf("read commits in %s: %w", rev, err)
	}
	return ParseLog(out), nil
}

// ParseLog reads the record stream the log format produces.
func ParseLog(out string) []Commit {
	var commits []Commit
	for record := range strings.SplitSeq(out, recordSep) {
		record = strings.Trim(record, "\n")
		if strings.TrimSpace(record) == "" {
			continue
		}
		fields := strings.SplitN(record, fieldSep, 3)
		if len(fields) < 2 {
			continue
		}
		c := Commit{SHA: strings.TrimSpace(fields[0]), Subject: strings.TrimSpace(fields[1])}
		if len(fields) == 3 {
			c.Body = strings.TrimSpace(fields[2])
		}
		commits = append(commits, c)
	}
	return commits
}

// TagExists reports whether a tag is already present locally.
func TagExists(ctx context.Context, r Runner, tag string) bool {
	res := r.Run(ctx, git("rev-parse", "--verify", "--quiet", "refs/tags/"+tag))
	return res.Err == nil && strings.TrimSpace(res.Output) != ""
}

// WorkingTreeClean reports whether the tree has uncommitted changes. A tag cut
// from a dirty tree points at a commit that does not describe what was tested.
func WorkingTreeClean(ctx context.Context, r Runner) (bool, error) {
	out, err := output(ctx, r, git("status", "--porcelain"))
	if err != nil {
		return false, fmt.Errorf("read working tree state: %w", err)
	}
	return strings.TrimSpace(out) == "", nil
}

// CreateAndPushTag creates an annotated tag at sha and pushes it to remote.
// The tag is annotated so it carries an author and a date, which a lightweight
// tag does not.
func CreateAndPushTag(ctx context.Context, r Runner, remote, tag, sha, message string) error {
	if res := r.Run(ctx, git("tag", "-a", tag, sha, "-m", message)); res.Err != nil {
		return fmt.Errorf("create tag %s: %w", tag, res.Err)
	}
	if res := r.Run(ctx, git("push", remote, "refs/tags/"+tag)); res.Err != nil {
		return fmt.Errorf("push tag %s to %s: %w", tag, remote, res.Err)
	}
	return nil
}
