package main

import (
	"strings"
	"testing"
)

func TestIsPinned(t *testing.T) {
	digest := strings.Repeat("a", 40)
	cases := map[string]bool{
		"actions/checkout@" + digest:                        true,
		"actions/cache/restore@" + digest:                   true,
		"./.github/workflows/verify.yml":                    true,
		"docker://alpine@sha256:" + strings.Repeat("b", 64): true,
		"actions/checkout@v4":                               false,
		"actions/checkout@main":                             false,
		"actions/checkout":                                  false,
		"actions/checkout@" + strings.Repeat("a", 7):        false,
		"docker://alpine:3.20":                              false,
		"owner/repo@" + strings.ToUpper(digest):             false,
	}
	for ref, want := range cases {
		if got := IsPinned(ref); got != want {
			t.Errorf("IsPinned(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestCheckActionPinsFindsTagReferences(t *testing.T) {
	dir := t.TempDir()
	digest := strings.Repeat("a", 40)
	writeFile(t, dir, "release.yml", `jobs:
  build:
    steps:
      - uses: actions/checkout@`+digest+` # v4.2.2
      - uses: actions/setup-go@v5
      - name: build
        run: make build
`)
	writeFile(t, dir, "notes.md", "- uses: actions/checkout@v4\n")

	problems, err := CheckActionPins(dir)
	if err != nil {
		t.Fatalf("CheckActionPins: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want only the tag reference", problems)
	}
	if !problemsContain(problems, "actions/setup-go@v5") || !problemsContain(problems, "release.yml:5") {
		t.Errorf("the problem does not name the reference and its line: %v", problems)
	}
}

func TestCheckActionPinsAcceptsAFullyPinnedDirectory(t *testing.T) {
	dir := t.TempDir()
	digest := strings.Repeat("f", 40)
	writeFile(t, dir, "verify.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@"+digest+"\n")
	problems, err := CheckActionPins(dir)
	if err != nil {
		t.Fatalf("CheckActionPins: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestCheckActionPinsReportsAMissingDirectory(t *testing.T) {
	if _, err := CheckActionPins(t.TempDir() + "/absent"); err == nil {
		t.Fatal("CheckActionPins accepted a directory that does not exist")
	}
}

func TestFindActionRefsReadsQuotedAndListedForms(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "steps:\n  - uses: 'owner/one@v1'\n    with: {}\n  - uses: \"owner/two@v2\"\n")
	refs, err := FindActionRefs(dir)
	if err != nil {
		t.Fatalf("FindActionRefs: %v", err)
	}
	if len(refs) != 2 || refs[0].Ref != "owner/one@v1" || refs[1].Ref != "owner/two@v2" {
		t.Fatalf("refs = %+v", refs)
	}
}
