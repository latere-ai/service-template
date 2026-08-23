package main

import (
	"strings"
	"testing"
)

func TestAnInternalReferenceInAPublishedDocumentFails(t *testing.T) {
	cases := map[string]string{
		"a spec file":    "# Title\n\nThe rule comes from specs/023-spec-driven-workflow.md.\n",
		"a spec number":  "# Title\n\nSee spec 014 for the reasoning.\n",
		"a tracker link": "# Title\n\nTracked at https://example.atlassian.net/browse/AB-12.\n",
		"a work item":    "# Title\n\nSee ticket 4821 for the history.\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeDocs(t, map[string]string{"README.md": body})
			problems := CheckReferences(dir)
			if len(problems) != 1 {
				t.Fatalf("want one problem, got %v", problems)
			}
			if !strings.Contains(problems[0], "README.md:3") {
				t.Errorf("the failure does not name the file and the line: %v", problems[0])
			}
		})
	}
}

// The rule reports identifiers a reader outside the team cannot resolve, not
// every token with a number in it. A check that fires on "UTF-8" is a check
// people switch off.
func TestOrdinaryTechnicalProseIsNotAnInternalReference(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md": "# Title\n\nBodies are UTF-8. Digests are SHA-256. HTTP/2 is supported.\n" +
			"The service answers 499 when the client disconnects, and Go 1.27 builds it.\n",
		"docs/a.md": "# A\n\nThe design notes live in the specs directory of this repository.\n",
	})
	if problems := CheckReferences(dir); len(problems) != 0 {
		t.Fatalf("ordinary prose was reported as an internal reference: %v", problems)
	}
}

func TestTheSpecDirectoryItselfIsNotChecked(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"specs/001-a.md": "# A\n\nThis one depends on specs/002-b.md.\n",
	})
	if problems := CheckReferences(dir); len(problems) != 0 {
		t.Fatalf("a spec was held to the published-document rule: %v", problems)
	}
}

// The published documents of this repository carry no reference a reader
// outside the team cannot resolve.
func TestCommittedPublishedDocumentsAreClean(t *testing.T) {
	if problems := CheckReferences(repoRoot); len(problems) != 0 {
		t.Fatalf("the published documents cite internal references:\n  %s", strings.Join(problems, "\n  "))
	}
}
