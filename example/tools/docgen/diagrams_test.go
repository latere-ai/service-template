package main

import (
	"strings"
	"testing"
)

// wrap puts a diagram body into a document, so a test states the diagram only.
func wrap(body string) string {
	return "# Title\n\nText.\n\n```mermaid\n" + body + "\n```\n"
}

func TestAMalformedDiagramFailsWithItsFileAndLine(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "an unclosed bracket",
			doc:  wrap("flowchart LR\n  a[Start --> b[End]"),
			want: "does not close it",
		},
		{
			name: "an unknown diagram type",
			doc:  wrap("flowchat LR\n  a --> b"),
			want: "is not a mermaid diagram type",
		},
		{
			name: "an empty block",
			doc:  wrap(""),
			want: "the mermaid block is empty",
		},
		{
			name: "a block that is never closed",
			doc:  "# Title\n\n```mermaid\nflowchart LR\n  a --> b\n",
			want: "never closed",
		},
		{
			name: "an unclosed label",
			doc:  wrap("flowchart LR\n  a[\"Start] --> b"),
			want: "quoted label",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeDocs(t, map[string]string{"docs/architecture.md": c.doc})
			problems := CheckDiagrams(dir, "")
			if len(problems) != 1 {
				t.Fatalf("want one problem, got %v", problems)
			}
			if !strings.Contains(problems[0], c.want) {
				t.Errorf("the failure does not say what is wrong: %v", problems[0])
			}
			if !strings.Contains(problems[0], "architecture.md:") {
				t.Errorf("the failure does not name the file and the line: %v", problems[0])
			}
		})
	}
}

func TestAValidDiagramPasses(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"docs/a.md": wrap("flowchart LR\n  a[Start] --> b{Choice}\n  b -->|yes| c[(Store)]"),
		"docs/b.md": wrap("sequenceDiagram\n  participant A\n  A->>B: request\n  B-->>A: response"),
		"docs/c.md": wrap("%%{init: {\"theme\": \"neutral\"}}%%\nstateDiagram-v2\n  [*] --> drafted"),
	})
	if problems := CheckDiagrams(dir, ""); len(problems) != 0 {
		t.Fatalf("a valid diagram was reported: %v", problems)
	}
}

func TestANonMermaidFenceIsNotADiagram(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"docs/a.md": "# Title\n\n```sh\nmake dev [\n```\n",
	})
	if problems := CheckDiagrams(dir, ""); len(problems) != 0 {
		t.Fatalf("a shell block was checked as a diagram: %v", problems)
	}
}

// The diagrams committed in this repository pass the structural rules.
func TestCommittedDiagramsAreWellFormed(t *testing.T) {
	diagrams, problems, err := CollectDiagrams(repoRoot)
	if err != nil {
		t.Fatalf("collect the diagrams: %v", err)
	}
	if len(diagrams) == 0 {
		t.Fatal("the documentation set carries no diagram")
	}
	if len(problems) != 0 {
		t.Fatalf("the committed diagrams are malformed:\n  %s", strings.Join(problems, "\n  "))
	}
	if problems := CheckDiagrams(repoRoot, ""); len(problems) != 0 {
		t.Fatalf("the committed diagrams failed the check:\n  %s", strings.Join(problems, "\n  "))
	}
}
