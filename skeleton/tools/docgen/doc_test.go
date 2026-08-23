package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the repository the checks run against from this package.
const repoRoot = "../.."

// generateInto renders the derived documents into a throwaway directory.
func generateInto(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := WriteDocs(dir); err != nil {
		t.Fatalf("generate the documents: %v", err)
	}
	return dir
}

func TestGenerationIsDeterministic(t *testing.T) {
	for _, d := range Documents() {
		first, err := d.Render()
		if err != nil {
			t.Fatalf("render %s: %v", d.Name, err)
		}
		second, err := d.Render()
		if err != nil {
			t.Fatalf("render %s a second time: %v", d.Name, err)
		}
		if string(first) != string(second) {
			t.Errorf("two renders of %s differ", d.Name)
		}
	}
}

func TestStaleDocumentFailsTheCheckAndRegenerationClearsIt(t *testing.T) {
	dir := generateInto(t)
	if err := CheckDocs(dir); err != nil {
		t.Fatalf("the freshly generated documents failed the check: %v", err)
	}

	path := filepath.Join(dir, "configuration.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the document: %v", err)
	}
	edited := strings.Replace(string(data), "`ADDR`", "`LISTEN_ADDRESS`", 1)
	if edited == string(data) {
		t.Fatal("the document does not name the field the test edits")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write the edited document: %v", err)
	}

	err = CheckDocs(dir)
	if !errors.Is(err, ErrDocStale) {
		t.Fatalf("an edited document passed the check: %v", err)
	}
	if !strings.Contains(err.Error(), "make docs") {
		t.Errorf("the failure does not name the command that fixes it: %v", err)
	}

	if _, err := WriteDocs(dir); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if err := CheckDocs(dir); err != nil {
		t.Fatalf("the regenerated documents failed the check: %v", err)
	}
}

func TestMissingDocumentFailsTheCheck(t *testing.T) {
	dir := generateInto(t)
	if err := os.Remove(filepath.Join(dir, "api.md")); err != nil {
		t.Fatalf("remove the document: %v", err)
	}
	if err := CheckDocs(dir); !errors.Is(err, ErrDocStale) {
		t.Fatalf("a missing document passed the check: %v", err)
	}
}

// The copies committed in this repository are what a reader opens, so they are
// held to the same rule as a consumer's copies.
func TestCommittedDocumentsMatchTheCode(t *testing.T) {
	if err := CheckDocs(filepath.Join(repoRoot, "docs")); err != nil {
		t.Fatalf("the committed documents do not match the code: %v", err)
	}
}
