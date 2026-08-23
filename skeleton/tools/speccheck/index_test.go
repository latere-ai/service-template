package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexIsDeterministicAndOrderedByNumber(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"003-c.md", full("C", "drafted", "", "")},
		specFile{"001-a.md", full("A", "drafted", "", "")},
		specFile{".archive/002-b.md", full("B", "superseded", "", "")},
	)
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	first := Index(specs)
	if second := Index(specs); !bytes.Equal(first, second) {
		t.Fatal("two renders of the same specs differ")
	}

	text := string(first)
	posA := strings.Index(text, "001-a.md")
	posC := strings.Index(text, "003-c.md")
	posArchive := strings.Index(text, "## Archived")
	posB := strings.Index(text, "002-b.md")
	switch {
	case posA < 0 || posC < 0 || posB < 0:
		t.Fatalf("the index does not list every spec:\n%s", text)
	case posA > posC:
		t.Errorf("the index is not ordered by number:\n%s", text)
	case posB < posArchive:
		t.Errorf("an archived spec is listed in the work queue:\n%s", text)
	}
}

func TestStaleIndexFailsAndRegenerates(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", full("A", "drafted", "", "")})
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := CheckIndex(dir, specs); !errors.Is(err, ErrIndexStale) {
		t.Fatalf("a missing index passed the check: %v", err)
	}
	if err := WriteIndex(dir, specs); err != nil {
		t.Fatalf("write the index: %v", err)
	}
	if err := CheckIndex(dir, specs); err != nil {
		t.Fatalf("the freshly written index failed the check: %v", err)
	}

	// A spec added without regenerating leaves the index behind, which is the
	// case the check exists for.
	path := filepath.Join(dir, "002-b.md")
	if err := os.WriteFile(path, []byte(full("B", "drafted", "", "")), 0o644); err != nil {
		t.Fatalf("write the second spec: %v", err)
	}
	specs, err = Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	err = CheckIndex(dir, specs)
	if !errors.Is(err, ErrIndexStale) {
		t.Fatalf("a stale index passed the check: %v", err)
	}
	if !strings.Contains(err.Error(), "make spec-index") {
		t.Errorf("the failure does not name the command that fixes it: %v", err)
	}

	if err := WriteIndex(dir, specs); err != nil {
		t.Fatalf("regenerate the index: %v", err)
	}
	if err := CheckIndex(dir, specs); err != nil {
		t.Fatalf("the regenerated index failed the check: %v", err)
	}
}

func TestTitleWithATableCharacterDoesNotBreakTheRow(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", full("A | B", "drafted", "", "")})
	specs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(string(Index(specs)), `A \| B`) {
		t.Fatalf("the title is not escaped:\n%s", Index(specs))
	}
}
