package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specFile is one file in a throwaway spec directory.
type specFile struct {
	name string
	body string
}

// full renders a spec with every required field and section, so a test states
// only the part it is about.
func full(title, status, dependsOn, extra string) string {
	return "---\n" +
		"title: " + title + "\n" +
		"status: " + status + "\n" +
		"depends_on: [" + dependsOn + "]\n" +
		"affects: [internal/]\n" +
		"created: 2026-01-01\n" +
		"author: service-team\n" +
		"trigger: a test\n" +
		"---\n\n" +
		"# " + title + "\n\n" +
		"## Problem\n\nA problem.\n\n" +
		"## Scope\n\nA scope.\n\n" +
		"## Design\n\nA design.\n\n" +
		"## Acceptance criteria\n\n1. A criterion.\n" +
		extra
}

// writeSpecs lays out a spec directory and returns its path.
func writeSpecs(t *testing.T, files ...specFile) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", f.name, err)
		}
		if err := os.WriteFile(path, []byte(f.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
	}
	return dir
}

// problems loads and validates a directory.
func problems(t *testing.T, dir string) []string {
	t.Helper()
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	return Validate(specs)
}

// contains reports whether any problem names the substring.
func contains(list []string, want string) bool {
	for _, p := range list {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

func TestMissingFrontmatterFieldFails(t *testing.T) {
	body := full("A spec", "drafted", "", "")
	stripped := strings.Replace(body, "trigger: a test\n", "", 1)
	dir := writeSpecs(t, specFile{"001-a.md", stripped})

	got := problems(t, dir)
	if !contains(got, "frontmatter has no trigger field") {
		t.Fatalf("a missing required field passed validation: %v", got)
	}

	// The same file with the field present passes, so the rule reports the
	// field and not the file.
	dir = writeSpecs(t, specFile{"001-a.md", body})
	if got := problems(t, dir); len(got) != 0 {
		t.Fatalf("a complete spec failed validation: %v", got)
	}
}

func TestEmptyFrontmatterValueFails(t *testing.T) {
	body := strings.Replace(full("A spec", "drafted", "", ""), "author: service-team", "author:", 1)
	got := problems(t, writeSpecs(t, specFile{"001-a.md", body}))
	if !contains(got, "field author is empty") {
		t.Fatalf("an empty required value passed validation: %v", got)
	}
}

func TestUnknownStatusFails(t *testing.T) {
	got := problems(t, writeSpecs(t, specFile{"001-a.md", full("A spec", "started", "", "")}))
	if !contains(got, `status "started" is not one of`) {
		t.Fatalf("an unknown status passed validation: %v", got)
	}
}

func TestDependencyCycleFailsAndNamesTheCycle(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "drafted", "specs/002-b.md", "")},
		specFile{"002-b.md", full("B", "drafted", "specs/003-c.md", "")},
		specFile{"003-c.md", full("C", "drafted", "specs/001-a.md", "")},
	)
	got := problems(t, dir)
	if !contains(got, "dependency cycle:") {
		t.Fatalf("a cycle passed validation: %v", got)
	}
	for _, name := range []string{"001-a.md", "002-b.md", "003-c.md"} {
		if !contains(got, name) {
			t.Errorf("the cycle report does not name %s: %v", name, got)
		}
	}
}

func TestUnresolvedDependencyFails(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", full("A", "drafted", "specs/099-gone.md", "")})
	if got := problems(t, dir); !contains(got, "does not resolve to a spec") {
		t.Fatalf("a dangling dependency passed validation: %v", got)
	}
}

func TestDispatchedWithIncompleteDependencyFails(t *testing.T) {
	dep := func(status string) string { return full("B", status, "", "\n## Outcome\n\nIt shipped.\n") }
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "dispatched", "specs/002-b.md", "")},
		specFile{"002-b.md", dep("in-progress")},
	)
	got := problems(t, dir)
	if !contains(got, "work starts when its dependencies are complete") {
		t.Fatalf("a dispatched spec with an open dependency passed validation: %v", got)
	}

	dir = writeSpecs(t,
		specFile{"001-a.md", full("A", "dispatched", "specs/002-b.md", "")},
		specFile{"002-b.md", dep("complete")},
	)
	if got := problems(t, dir); len(got) != 0 {
		t.Fatalf("a dispatched spec with a complete dependency failed: %v", got)
	}
}

func TestCompleteWithoutOutcomeFails(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", full("A", "complete", "", "")})
	if got := problems(t, dir); !contains(got, "no Outcome section") {
		t.Fatalf("a complete spec with no Outcome passed validation: %v", got)
	}

	dir = writeSpecs(t, specFile{"001-a.md", full("A", "complete", "", "\n## Outcome\n\nIt shipped.\n")})
	if got := problems(t, dir); len(got) != 0 {
		t.Fatalf("a complete spec with an Outcome failed: %v", got)
	}
}

func TestSectionOrderIsEnforced(t *testing.T) {
	body := "---\ntitle: A\nstatus: drafted\ndepends_on: []\naffects: []\ncreated: 2026-01-01\n" +
		"author: service-team\ntrigger: a test\n---\n\n" +
		"## Design\n\nD\n\n## Problem\n\nP\n\n## Scope\n\nS\n\n## Acceptance criteria\n\n1. C\n"
	got := problems(t, writeSpecs(t, specFile{"001-a.md", body}))
	if !contains(got, "comes after") {
		t.Fatalf("an out-of-order body passed validation: %v", got)
	}
}

func TestArchivingKeepsTheNumberAndInboundReferences(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "drafted", "specs/002-b.md", "")},
		specFile{".archive/002-b.md", full("B", "complete", "", "\n## Outcome\n\nIt shipped.\n")},
	)
	if got := problems(t, dir); len(got) != 0 {
		t.Fatalf("a reference into the archive failed to resolve: %v", got)
	}

	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, s := range specs {
		if s.Name == "002-b.md" && (s.Number != 2 || !s.Archived) {
			t.Fatalf("archiving changed the identity of the spec: number %d, archived %v", s.Number, s.Archived)
		}
	}
}

func TestReferenceByNumberResolvesAfterARename(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "drafted", "specs/002-old-name.md", "")},
		specFile{"002-new-name.md", full("B", "drafted", "", "")},
	)
	if got := problems(t, dir); len(got) != 0 {
		t.Fatalf("a reference by number failed to resolve: %v", got)
	}
}

func TestReusedNumberFails(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "drafted", "", "")},
		specFile{"001-b.md", full("B", "drafted", "", "")},
	)
	if got := problems(t, dir); !contains(got, "numbers are stable identifiers") {
		t.Fatalf("a reused number passed validation: %v", got)
	}
}

func TestUnnumberedFileFails(t *testing.T) {
	dir := writeSpecs(t, specFile{"notes.md", full("A", "drafted", "", "")})
	if got := problems(t, dir); !contains(got, "file name is not NNN-name.md") {
		t.Fatalf("an unnumbered spec passed validation: %v", got)
	}
}
