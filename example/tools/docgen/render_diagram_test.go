//go:build integration

// Rendering a diagram runs in the integration tier. It needs the mermaid
// command line, and a dependency that a tier can decide to skip at run time
// belongs where CI declares the dependency required.
package main

import (
	"os/exec"
	"testing"

	"github.com/example/reference-service/internal/testsupport"
)

// Rendering is the only check that proves the whole grammar. It needs the
// mermaid command line, so it is a required dependency of the tier that
// declares one and a skip elsewhere.
func TestCommittedDiagramsRender(t *testing.T) {
	renderer := requireMermaid(t)
	diagrams, _, err := CollectDiagrams(repoRoot)
	if err != nil {
		t.Fatalf("collect the diagrams: %v", err)
	}
	for _, d := range diagrams {
		if err := Render(t.Context(), renderer, d); err != nil {
			t.Errorf("%s:%d does not render: %v", d.File, d.Line, err)
		}
	}
}

// A diagram the structural rules accept but mermaid refuses is reported by the
// renderer, which is what proves the render step is a gate and not a formality.
func TestTheRendererRejectsWhatMermaidCannotDraw(t *testing.T) {
	renderer := requireMermaid(t)
	broken := Diagram{
		File: "docs/broken.md", Line: 1,
		Body: "sequenceDiagram\n  participant A\n  A->>: request",
	}
	if err := Render(t.Context(), renderer, broken); err == nil {
		t.Fatal("mermaid drew a diagram with a syntax error")
	}
}

// requireMermaid resolves the mermaid command line under the dependency rule:
// required mode fails and names it, optional mode skips.
func requireMermaid(t *testing.T) string {
	t.Helper()
	for _, name := range mermaidCommands {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return testsupport.RequireBinary(t, mermaidCommands[0])
}
