package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontmatterSubsetIsParsed(t *testing.T) {
	body := "---\n" +
		"# a comment\n" +
		"title: \"A quoted title\"\n" +
		"status: 'drafted'\n" +
		"depends_on: [specs/002-b.md, specs/003-c.md]\n" +
		"affects:\n" +
		"  - internal/a\n" +
		"  - internal/b\n" +
		"created: 2026-01-01\n" +
		"author: service-team\n" +
		"trigger: a test\n" +
		"---\n\n" +
		"## Problem\n\nP\n\n## Scope\n\nS\n\n## Design\n\nD\n\n## Acceptance criteria\n\n1. C\n"

	dir := writeSpecs(t,
		specFile{"001-a.md", body},
		specFile{"002-b.md", full("B", "drafted", "", "")},
		specFile{"003-c.md", full("C", "drafted", "", "")},
	)
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var spec *Spec
	for _, s := range specs {
		if s.Name == "001-a.md" {
			spec = s
		}
	}
	if spec == nil {
		t.Fatal("the spec was not loaded")
	}
	switch {
	case spec.Title != "A quoted title":
		t.Errorf("the quoted title was not read: %q", spec.Title)
	case spec.Status != StatusDrafted:
		t.Errorf("the quoted status was not read: %q", spec.Status)
	case len(spec.DependsOn) != 2:
		t.Errorf("the inline list was not read: %v", spec.DependsOn)
	case len(spec.Affects) != 2:
		t.Errorf("the block list was not read: %v", spec.Affects)
	}
	if problems := Validate(specs); len(problems) != 0 {
		t.Fatalf("the spec failed validation: %v", problems)
	}

	// The index reports the dependencies of a spec, so the queue shows what
	// blocks what without opening the files.
	index := string(Index(specs))
	if !strings.Contains(index, "002-b.md, specs/003-c.md") && !strings.Contains(index, "002-b.md") {
		t.Errorf("the index does not report the dependencies:\n%s", index)
	}
}

func TestAnUnparsableFrontmatterIsReported(t *testing.T) {
	cases := map[string]string{
		"a line that is not a pair": "---\ntitle: A\nnot a pair\n---\n",
		"a list with no key":        "---\n  - orphan\ntitle: A\n---\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeSpecs(t, specFile{"001-a.md", body})
			if _, err := Load(dir); err == nil {
				t.Fatal("an unparsable header was accepted")
			}
		})
	}
}

func TestAFileWithNoFrontmatterFailsOnItsMissingFields(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", "# A spec\n\nNo header at all.\n"})
	problems := problems(t, dir)
	if len(problems) < len(requiredFields) {
		t.Fatalf("a file with no header reported %d problems: %v", len(problems), problems)
	}
}

func TestAnUnterminatedFrontmatterIsTreatedAsHeaderOnly(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", "---\ntitle: A\nstatus: drafted\n"})
	if got := problems(t, dir); !contains(got, "body has no Problem section") {
		t.Fatalf("an unterminated header did not report its missing body: %v", got)
	}
}

func TestTheIndexCannotBeWrittenToAMissingDirectory(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", full("A", "drafted", "", "")})
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := WriteIndex(filepath.Join(dir, "absent"), specs); err == nil {
		t.Fatal("the index was written into a directory that does not exist")
	}
}

func TestLoadReportsAnUnreadableDirectory(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing spec directory was accepted")
	}
}

// A subdirectory that is not the archive holds no specs, so a working note
// beside the queue is not validated as one.
func TestOnlyTheArchiveSubdirectoryIsRead(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "drafted", "", "")},
		specFile{"notes/draft.md", "not a spec"},
	)
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("want one spec, got %d", len(specs))
	}
	if err := os.WriteFile(filepath.Join(dir, IndexName), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write the index: %v", err)
	}
	if specs, err = Load(dir); err != nil || len(specs) != 1 {
		t.Fatalf("the index was read as a spec: %d specs, %v", len(specs), err)
	}
}
