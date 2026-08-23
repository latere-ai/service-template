package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Diagram is one mermaid block in a document, with the line its fence opens
// on, so a failure points at a place in an editor.
type Diagram struct {
	File string
	Line int
	Body string
}

// diagramTypes are the mermaid diagram declarations this check accepts. A
// block that opens with anything else is a diagram mermaid will not draw, and
// an undrawn diagram renders as an empty space rather than as an error, which
// is why it is checked here instead of being noticed in review.
var diagramTypes = []string{
	"architecture-beta",
	"block-beta",
	"classDiagram",
	"erDiagram",
	"flowchart",
	"gantt",
	"gitGraph",
	"graph",
	"journey",
	"mindmap",
	"pie",
	"quadrantChart",
	"requirementDiagram",
	"sankey-beta",
	"sequenceDiagram",
	"stateDiagram-v2",
	"stateDiagram",
	"timeline",
	"xychart-beta",
	"C4Context",
}

// CollectDiagrams returns every mermaid block in the Markdown files under root.
func CollectDiagrams(root string) ([]Diagram, []string, error) {
	files, err := MarkdownFiles(root)
	if err != nil {
		return nil, nil, err
	}
	var diagrams []Diagram
	var problems []string
	for _, file := range files {
		text, err := readFile(file)
		if err != nil {
			return nil, nil, err
		}
		found, fileProblems := diagramsIn(file, text)
		diagrams = append(diagrams, found...)
		problems = append(problems, fileProblems...)
	}
	return diagrams, problems, nil
}

// diagramsIn extracts the mermaid blocks of one document.
func diagramsIn(file, text string) ([]Diagram, []string) {
	var diagrams []Diagram
	var problems []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		marker, language := m[1], m[2]
		start := i
		var body []string
		closed := false
		for i++; i < len(lines); i++ {
			if end := fenceRE.FindStringSubmatch(lines[i]); end != nil && strings.HasPrefix(end[1], marker[:1]) && end[2] == "" {
				closed = true
				break
			}
			body = append(body, lines[i])
		}
		if !strings.EqualFold(language, "mermaid") {
			continue
		}
		if !closed {
			problems = append(problems, fmt.Sprintf("%s:%d: the mermaid block is never closed", file, start+1))
			continue
		}
		diagrams = append(diagrams, Diagram{File: file, Line: start + 1, Body: strings.Join(body, "\n")})
	}
	return diagrams, problems
}

// CheckDiagrams validates every mermaid block under root. The structural rules
// run always. When mermaid is on PATH the block is also rendered, which is the
// only check that proves the whole grammar.
func CheckDiagrams(root string, renderer string) []string {
	diagrams, problems, err := CollectDiagrams(root)
	if err != nil {
		return []string{fmt.Sprintf("read the documents: %v", err)}
	}
	for _, d := range diagrams {
		if problem := Structure(d); problem != "" {
			problems = append(problems, problem)
			continue
		}
		if renderer == "" {
			continue
		}
		if err := Render(renderer, d); err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: %v", d.File, d.Line, err))
		}
	}
	return problems
}

// Structure applies the rules a malformed diagram breaks before mermaid is
// reached: an empty block, an unknown diagram type, and unbalanced brackets.
func Structure(d Diagram) string {
	body := strings.TrimSpace(d.Body)
	if body == "" {
		return fmt.Sprintf("%s:%d: the mermaid block is empty", d.File, d.Line)
	}

	declaration, offset := firstStatement(d.Body)
	if declaration == "" {
		return fmt.Sprintf("%s:%d: the mermaid block declares no diagram type", d.File, d.Line)
	}
	if !known(declaration) {
		return fmt.Sprintf("%s:%d: %q is not a mermaid diagram type; expected one of %s",
			d.File, d.Line+offset, firstWord(declaration), strings.Join(diagramTypes, ", "))
	}

	if line, problem := brackets(d.Body); problem != "" {
		return fmt.Sprintf("%s:%d: %s", d.File, d.Line+line, problem)
	}
	return ""
}

// firstStatement returns the first line that is neither blank nor a directive,
// and its offset inside the block.
func firstStatement(body string) (string, int) {
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		return trimmed, i + 1
	}
	return "", 0
}

// known reports whether a statement opens a diagram this check accepts.
func known(statement string) bool {
	return slices.Contains(diagramTypes, firstWord(statement))
}

// firstWord returns the leading token of a statement, which is the diagram
// declaration when there is one.
func firstWord(statement string) string {
	if i := strings.IndexAny(statement, " \t"); i > 0 {
		return statement[:i]
	}
	return statement
}

// brackets reports the first unbalanced bracket in a diagram and the line it
// is on. Text inside quotes is skipped, because a label may hold anything.
func brackets(body string) (int, string) {
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	var stack []rune
	var openedAt []int
	inQuote := false
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "%%") {
			continue
		}
		for _, r := range line {
			switch {
			case r == '"':
				inQuote = !inQuote
			case inQuote:
			case r == '(' || r == '[' || r == '{':
				stack = append(stack, r)
				openedAt = append(openedAt, i+1)
			case r == ')' || r == ']' || r == '}':
				if len(stack) == 0 || stack[len(stack)-1] != pairs[r] {
					return i + 1, fmt.Sprintf("the diagram closes %q with nothing open", string(r))
				}
				stack = stack[:len(stack)-1]
				openedAt = openedAt[:len(openedAt)-1]
			}
		}
		if inQuote {
			return i + 1, "the diagram opens a quoted label and does not close it"
		}
	}
	if len(stack) > 0 {
		return openedAt[0], fmt.Sprintf("the diagram opens %q and does not close it", string(stack[0]))
	}
	return 0, ""
}

// Render draws the diagram with the mermaid command line. A diagram mermaid
// refuses to draw is reported with the message mermaid produced.
func Render(renderer string, d Diagram) error {
	dir, err := os.MkdirTemp("", "mermaid")
	if err != nil {
		return fmt.Errorf("create a working directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	input := filepath.Join(dir, "diagram.mmd")
	if err := os.WriteFile(input, []byte(d.Body+"\n"), 0o600); err != nil {
		return fmt.Errorf("write the diagram: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, renderer, "-i", input, "-o", filepath.Join(dir, "diagram.svg"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mermaid did not render the diagram: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
