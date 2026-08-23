package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// block identifies one statement block in a coverage profile. Two tiers that
// both exercise a block report it under the same key, so merging is a set
// union rather than a sum of overlapping numbers.
type block struct {
	file  string
	span  string
	stmts int
}

// profileSet accumulates the blocks of one or more coverage profiles. A block
// counts as covered when any tier reached it, which is what lets the unit and
// integration figures combine into one number.
type profileSet struct {
	stmts   map[block]bool
	ordered []block
}

func newProfileSet() *profileSet {
	return &profileSet{stmts: map[block]bool{}}
}

// Add parses one coverage profile. The first line declares the mode and every
// later line is "importpath/file.go:startLine.startCol,endLine.endCol stmts count".
func (p *profileSet) Add(name string, r io.Reader) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for s.Scan() {
		line++
		text := strings.TrimSpace(s.Text())
		if text == "" {
			continue
		}
		if line == 1 && strings.HasPrefix(text, "mode:") {
			continue
		}
		b, count, err := parseLine(text)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", name, line, err)
		}
		if _, seen := p.stmts[b]; !seen {
			p.ordered = append(p.ordered, b)
		}
		p.stmts[b] = p.stmts[b] || count > 0
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	return nil
}

func parseLine(text string) (block, int, error) {
	fields := strings.Fields(text)
	if len(fields) != 3 {
		return block{}, 0, fmt.Errorf("expected three fields, found %d in %q", len(fields), text)
	}
	colon := strings.LastIndex(fields[0], ":")
	if colon < 0 {
		return block{}, 0, fmt.Errorf("no file and span separator in %q", fields[0])
	}
	stmts, err := strconv.Atoi(fields[1])
	if err != nil {
		return block{}, 0, fmt.Errorf("statement count %q: %w", fields[1], err)
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return block{}, 0, fmt.Errorf("execution count %q: %w", fields[2], err)
	}
	return block{file: fields[0][:colon], span: fields[0][colon+1:], stmts: stmts}, count, nil
}

// PackageCoverage is the merged figure for one package.
type PackageCoverage struct {
	// ImportPath is the package as the profile names it.
	ImportPath string
	// Path is ImportPath relative to the repository root, which is the form
	// the exclusion list is written in.
	Path string
	// Statements is the number of statements the package holds.
	Statements int
	// Covered is how many of them at least one tier reached.
	Covered int
	// Excluded reports whether the declaration removes this package from the
	// denominator.
	Excluded bool
}

// Percent is the statement coverage of the package. A package with no
// statements is reported as fully covered, because there is nothing to reach.
func (p PackageCoverage) Percent() float64 {
	if p.Statements == 0 {
		return 100
	}
	return 100 * float64(p.Covered) / float64(p.Statements)
}

// Packages folds the accumulated blocks into one entry per package, ordered by
// repository-relative path.
func (p *profileSet) Packages(module string) []PackageCoverage {
	byPath := map[string]*PackageCoverage{}
	for _, b := range p.ordered {
		importPath := path.Dir(b.file)
		pkg, ok := byPath[importPath]
		if !ok {
			pkg = &PackageCoverage{
				ImportPath: importPath,
				Path:       relativeTo(module, importPath),
			}
			byPath[importPath] = pkg
		}
		pkg.Statements += b.stmts
		if p.stmts[b] {
			pkg.Covered += b.stmts
		}
	}
	out := make([]PackageCoverage, 0, len(byPath))
	for _, pkg := range byPath {
		out = append(out, *pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// relativeTo strips the module prefix from an import path so the result can be
// compared against the exclusion list, which is written in repository paths.
func relativeTo(module, importPath string) string {
	if module == "" {
		return importPath
	}
	if importPath == module {
		return "."
	}
	if rest, ok := strings.CutPrefix(importPath, module+"/"); ok {
		return rest
	}
	return importPath
}
