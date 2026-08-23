package main

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := map[string]struct {
		in    string
		want  Version
		valid bool
	}{
		"tagged":      {"v1.2.3", Version{1, 2, 3, ""}, true},
		"bare":        {"1.2.3", Version{1, 2, 3, ""}, true},
		"pre-release": {"v2.0.0-rc.1", Version{2, 0, 0, "rc.1"}, true},
		"not a tag":   {"release-2", Version{}, false},
		"two parts":   {"v1.2", Version{}, false},
		"metadata":    {"v1.2.3+build", Version{}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseVersion(c.in)
			if c.valid != (err == nil) {
				t.Fatalf("ParseVersion(%q) error = %v, want valid: %v", c.in, err, c.valid)
			}
			if c.valid && got != c.want {
				t.Errorf("ParseVersion(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestVersionStringRoundTrips(t *testing.T) {
	for _, s := range []string{"v1.2.3", "v2.0.0-rc.1"} {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", s, err)
		}
		if v.String() != s {
			t.Errorf("round trip of %q gave %q", s, v.String())
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		commit Commit
		want   Bump
	}{
		"feature":            {Commit{Subject: "feat: add the widget endpoint"}, BumpMinor},
		"feature with scope": {Commit{Subject: "feat(api): add the widget endpoint"}, BumpMinor},
		"fix":                {Commit{Subject: "fix: close the pool on shutdown"}, BumpPatch},
		"breaking marker":    {Commit{Subject: "feat!: drop the v0 routes"}, BumpMajor},
		"breaking scoped":    {Commit{Subject: "refactor(api)!: rename the field"}, BumpMajor},
		"breaking footer":    {Commit{Subject: "fix: rename the field", Body: "BREAKING CHANGE: the field moved"}, BumpMajor},
		"breaking hyphen":    {Commit{Subject: "fix: rename", Body: "BREAKING-CHANGE: the field moved"}, BumpMajor},
		"unlabelled":         {Commit{Subject: "tidy the makefile"}, BumpPatch},
		"docs":               {Commit{Subject: "docs: describe the gate"}, BumpPatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Classify(c.commit); got != c.want {
				t.Errorf("Classify(%q/%q) = %s, want %s", c.commit.Subject, c.commit.Body, got, c.want)
			}
		})
	}
}

// The three bump cases, each derived from the same previous tag.
func TestNextVersionCoversEveryBump(t *testing.T) {
	cases := map[string]struct {
		commits []Commit
		want    string
		bump    Bump
	}{
		"patch": {[]Commit{{Subject: "fix: a"}, {Subject: "chore: b"}}, "v1.4.3", BumpPatch},
		"minor": {[]Commit{{Subject: "fix: a"}, {Subject: "feat: b"}}, "v1.5.0", BumpMinor},
		"major": {[]Commit{{Subject: "feat: a"}, {Subject: "fix!: b"}}, "v2.0.0", BumpMajor},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, bump, err := NextVersion("v1.4.2", c.commits)
			if err != nil {
				t.Fatalf("NextVersion: %v", err)
			}
			if got.String() != c.want || bump != c.bump {
				t.Errorf("NextVersion = %s (%s), want %s (%s)", got, bump, c.want, c.bump)
			}
		})
	}
}

// One breaking change in a batch of fixes still produces a major release.
func TestNextVersionTakesTheLargestBump(t *testing.T) {
	commits := []Commit{{Subject: "fix: a"}, {Subject: "feat: b"}, {Subject: "fix: c", Body: "BREAKING CHANGE: d"}}
	got, bump, err := NextVersion("v3.7.1", commits)
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if got.String() != "v4.0.0" || bump != BumpMajor {
		t.Errorf("NextVersion = %s (%s), want v4.0.0 (major)", got, bump)
	}
}

func TestNextVersionFromNoPreviousTag(t *testing.T) {
	got, _, err := NextVersion("", []Commit{{Subject: "feat: first"}})
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if got.String() != FirstVersion {
		t.Errorf("NextVersion = %s, want %s", got, FirstVersion)
	}
}

func TestNextVersionRefusesAnEmptyRange(t *testing.T) {
	if _, _, err := NextVersion("v1.0.0", nil); err == nil {
		t.Fatal("NextVersion produced a version with nothing to release")
	}
}

// A candidate is not a predecessor: the release after v1.2.0-rc.1 is v1.2.0.
func TestNextVersionDropsAPreReleaseSuffix(t *testing.T) {
	got, _, err := NextVersion("v1.2.0-rc.1", []Commit{{Subject: "fix: a"}})
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if got.String() != "v1.2.1" {
		t.Errorf("NextVersion = %s, want v1.2.1", got)
	}
}

func TestPreReleaseIsRecognized(t *testing.T) {
	v, err := ParseVersion("v1.2.0-rc.1")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if !v.IsPreRelease() {
		t.Error("IsPreRelease = false for a candidate")
	}
	if plain, _ := ParseVersion("v1.2.0"); plain.IsPreRelease() {
		t.Error("IsPreRelease = true for a release")
	}
}

func TestChangeSummaryGroupsByBump(t *testing.T) {
	commits := []Commit{
		{SHA: "aaaaaaaaaaaa", Subject: "feat: add the endpoint"},
		{SHA: "bbbbbbbbbbbb", Subject: "fix: close the pool"},
		{SHA: "cccccccccccc", Subject: "feat!: drop v0"},
	}
	summary := ChangeSummary("v1.0.0", commits)
	for _, want := range []string{"3 commit(s) since v1.0.0", "major:", "minor:", "patch:", "aaaaaaaa", "drop v0"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
}

func TestBumpString(t *testing.T) {
	for bump, want := range map[Bump]string{BumpMajor: "major", BumpMinor: "minor", BumpPatch: "patch"} {
		if bump.String() != want {
			t.Errorf("Bump(%d) = %q, want %q", bump, bump.String(), want)
		}
	}
}
