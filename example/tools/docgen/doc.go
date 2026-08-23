package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generatedNotice heads every derived document. A reader who edits the file by
// hand loses the edit at the next generation, so the file says where the
// content comes from.
const generatedNotice = "<!-- Generated from the code by \"make docs\". Do not edit. -->\n"

// ErrDocStale reports a committed document that no longer matches the code.
var ErrDocStale = errors.New("a generated document is out of date")

// Document is one document derived from the code.
type Document struct {
	// Name is the file name inside the documentation directory.
	Name string
	// Render produces the whole file. It is a pure function of the code, so
	// two runs on any machine produce identical bytes.
	Render func() ([]byte, error)
}

// Documents are the derived documents, in the order they are generated.
//
// A document is derived when the code already states its content: the
// configuration reference is the configuration struct, and the interface
// reference is the route table and the error envelope. Prose that explains why
// the parts fit together is written by hand, because no struct holds it.
func Documents() []Document {
	return []Document{
		{Name: "configuration.md", Render: RenderConfiguration},
		{Name: "api.md", Render: RenderAPI},
	}
}

// WriteDocs renders every derived document into dir.
func WriteDocs(dir string) ([]string, error) {
	var written []string
	for _, d := range Documents() {
		data, err := d.Render()
		if err != nil {
			return written, fmt.Errorf("render %s: %w", d.Name, err)
		}
		path := filepath.Join(dir, d.Name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

// CheckDocs reports every derived document in dir that differs from what the
// code renders. A missing file fails the same way a stale one does, because
// both mean the committed repository does not describe the code.
func CheckDocs(dir string) error {
	var problems []string
	for _, d := range Documents() {
		want, err := d.Render()
		if err != nil {
			return fmt.Errorf("render %s: %w", d.Name, err)
		}
		path := filepath.Join(dir, d.Name)
		got, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if !bytes.Equal(got, want) {
			problems = append(problems, fmt.Sprintf("%s differs from the code\n%s", path, firstDifference(got, want)))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  %s\nrun: make docs", ErrDocStale, strings.Join(problems, "\n  "))
}

// firstDifference names the first line that differs, so the failure says what
// changed instead of only that something did.
func firstDifference(got, want []byte) string {
	gotLines := strings.Split(string(got), "\n")
	wantLines := strings.Split(string(want), "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		g, w := lineAt(gotLines, i), lineAt(wantLines, i)
		if g != w {
			return fmt.Sprintf("  line %d\n    committed: %q\n    expected:  %q", i+1, g, w)
		}
	}
	return "  the files differ in trailing content"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// cell renders a value for a table cell, neutralizing the one character that
// would break the row and marking an empty value rather than leaving a hole.
func cell(v string) string {
	v = strings.ReplaceAll(v, "|", `\|`)
	if v == "" {
		return "none"
	}
	return v
}
