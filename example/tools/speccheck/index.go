package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// indexHeader introduces the generated index. It carries no date and no
// count, so two runs over the same files produce identical bytes.
const indexHeader = `# Specs

One file per aspect of the design, named NNN-name.md. The number is a stable
identifier: it is never reused and never reassigned, so a reference to a spec
keeps resolving after the file is archived.

The lifecycle runs drafted, validated, dispatched, in-progress, testing,
complete. A spec reaches complete only with an Outcome section that records
what shipped and where it diverged from the design. A spec that is abandoned
or replaced becomes superseded.

Open specs are the work queue. Terminal specs move into ` + "`.archive/`" + `, keeping
their number.

This file is generated from the frontmatter of the files below. Run
"make spec-index" after adding or changing a spec; "make spec-check" proves the
committed copy is current.
`

// ErrIndexStale reports an index that no longer matches the spec files.
var ErrIndexStale = errors.New("the spec index is out of date")

// Index renders the index of the given specs.
func Index(specs []*Spec) []byte {
	open, archived := partition(specs)

	var b bytes.Buffer
	b.WriteString(indexHeader)
	writeSection(&b, "Open", "", open)
	writeSection(&b, "Archived", ArchiveDir+"/", archived)
	return b.Bytes()
}

// partition splits the specs into the work queue and the archive, each sorted
// by number so the index reads as the numbering it describes.
func partition(specs []*Spec) (open, archived []*Spec) {
	for _, s := range specs {
		if s.Archived {
			archived = append(archived, s)
			continue
		}
		open = append(open, s)
	}
	byNumber := func(list []*Spec) {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Number != list[j].Number {
				return list[i].Number < list[j].Number
			}
			return list[i].Name < list[j].Name
		})
	}
	byNumber(open)
	byNumber(archived)
	return open, archived
}

// writeSection renders one table. An empty section states that it is empty
// rather than vanishing, because a missing heading reads as a missing spec.
func writeSection(b *bytes.Buffer, title, prefix string, specs []*Spec) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	if len(specs) == 0 {
		b.WriteString("None.\n")
		return
	}
	b.WriteString("| Number | Spec | Status | Depends on |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, s := range specs {
		fmt.Fprintf(b, "| %03d | [%s](%s%s) | %s | %s |\n",
			s.Number, escape(s.Title), prefix, s.Name, s.Status, dependsCell(s))
	}
}

// dependsCell renders the dependency numbers of a spec.
func dependsCell(s *Spec) string {
	if len(s.DependsOn) == 0 {
		return "none"
	}
	var out []string
	for _, ref := range s.DependsOn {
		out = append(out, escape(filepath.Base(ref)))
	}
	return strings.Join(out, ", ")
}

// escape neutralizes the one character that would break a table row.
func escape(v string) string { return strings.ReplaceAll(v, "|", `\|`) }

// WriteIndex renders the index and writes it into dir.
func WriteIndex(dir string, specs []*Spec) error {
	path := filepath.Join(dir, IndexName)
	if err := os.WriteFile(path, Index(specs), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// CheckIndex reports whether the committed index matches the spec files. A
// missing index fails the same way a stale one does, because both mean the
// directory listing and the index disagree.
func CheckIndex(dir string, specs []*Spec) error {
	path := filepath.Join(dir, IndexName)
	want := Index(specs)
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrIndexStale, path, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s differs from the spec files\n%s\nrun: make spec-index",
			ErrIndexStale, path, firstDifference(got, want))
	}
	return nil
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
