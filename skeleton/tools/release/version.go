package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Bump is how far the next version moves. The order matters: the release takes
// the largest bump any commit since the previous tag asks for.
type Bump int

// The three bumps, smallest first.
const (
	BumpPatch Bump = iota
	BumpMinor
	BumpMajor
)

// String names the bump for the release summary.
func (b Bump) String() string {
	switch b {
	case BumpMajor:
		return "major"
	case BumpMinor:
		return "minor"
	default:
		return "patch"
	}
}

// FirstVersion is the version a repository with no previous release takes. The
// bump rules need a predecessor, and inventing 1.0.0 for a first tag claims a
// stability promise the repository has not made.
const FirstVersion = "v0.1.0"

// Version is a semantic version with an optional pre-release suffix.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// String renders the version with its leading v, which is the form a git tag
// takes.
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// IsPreRelease reports whether the version carries a pre-release suffix. A
// pre-release deploys to the pre-production target and publishes as a
// pre-release.
func (v Version) IsPreRelease() bool { return v.Pre != "" }

// versionPattern matches a semantic version tag. Build metadata is rejected
// rather than ignored, because a tag carrying it would not round-trip.
var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// ParseVersion reads a semantic version tag.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a semantic version tag such as v1.2.3 or v1.2.3-rc.1", s)
	}
	nums := make([]int, 3)
	for i := range nums {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("parse %q: %w", s, err)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: m[4]}, nil
}

// Next applies a bump. A pre-release suffix is dropped, because the release
// after v1.2.0-rc.1 is v1.2.0 and not another candidate.
func (v Version) Next(b Bump) Version {
	base := Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
	switch b {
	case BumpMajor:
		return Version{Major: base.Major + 1}
	case BumpMinor:
		return Version{Major: base.Major, Minor: base.Minor + 1}
	default:
		return Version{Major: base.Major, Minor: base.Minor, Patch: base.Patch + 1}
	}
}

// Commit is one commit since the previous tag.
type Commit struct {
	SHA     string
	Subject string
	Body    string
}

// headerPattern matches a conventional-commit subject: a type, an optional
// scope, an optional breaking marker, and the description.
var headerPattern = regexp.MustCompile(`^([a-zA-Z]+)(\([^)]*\))?(!)?:\s*(.+)$`)

// breakingNote is the footer form of a breaking change. Both spellings are
// accepted because both appear in the specification.
var breakingNote = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)

// Classify reads the bump one commit asks for. A subject that is not a
// conventional commit asks for a patch: an unlabelled change is still a
// change, and treating it as no change would let a release omit it.
func Classify(c Commit) Bump {
	if breakingNote.MatchString(c.Body) {
		return BumpMajor
	}
	m := headerPattern.FindStringSubmatch(strings.TrimSpace(c.Subject))
	if m == nil {
		return BumpPatch
	}
	if m[3] == "!" {
		return BumpMajor
	}
	if strings.EqualFold(m[1], "feat") {
		return BumpMinor
	}
	return BumpPatch
}

// NextVersion derives the version a release from these commits would take. The
// largest bump wins, so one breaking change in a batch of fixes still produces
// a major release.
func NextVersion(previous string, commits []Commit) (Version, Bump, error) {
	if len(commits) == 0 {
		return Version{}, BumpPatch, fmt.Errorf("no commits since %s, there is nothing to release", describePrevious(previous))
	}
	bump := BumpPatch
	for _, c := range commits {
		if b := Classify(c); b > bump {
			bump = b
		}
	}
	if strings.TrimSpace(previous) == "" {
		first, err := ParseVersion(FirstVersion)
		return first, bump, err
	}
	prev, err := ParseVersion(previous)
	if err != nil {
		return Version{}, bump, err
	}
	return prev.Next(bump), bump, nil
}

// describePrevious names the previous tag for a message, or says there was
// none.
func describePrevious(previous string) string {
	if strings.TrimSpace(previous) == "" {
		return "the start of history"
	}
	return previous
}

// ChangeSummary renders what moved since the previous tag, grouped by the bump
// each commit asked for, so the number the pipeline proposes is explainable.
func ChangeSummary(previous string, commits []Commit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d commit(s) since %s\n", len(commits), describePrevious(previous))
	for _, group := range []Bump{BumpMajor, BumpMinor, BumpPatch} {
		var lines []string
		for _, c := range commits {
			if Classify(c) == group {
				lines = append(lines, fmt.Sprintf("  %s %s", short(c.SHA), c.Subject))
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n%s\n", group, strings.Join(lines, "\n"))
	}
	return b.String()
}

// short renders the abbreviated commit a reader recognizes.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
