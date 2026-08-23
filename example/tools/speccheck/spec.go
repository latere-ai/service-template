package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Status is the lifecycle state of a spec.
type Status string

// The lifecycle states, in the order a spec moves through them. A state
// outside this set is a typo, and a typo in a status is how a spec disappears
// from the work queue without anyone deciding it should.
const (
	StatusDrafted    Status = "drafted"
	StatusValidated  Status = "validated"
	StatusDispatched Status = "dispatched"
	StatusInProgress Status = "in-progress"
	StatusTesting    Status = "testing"
	StatusComplete   Status = "complete"
	StatusSuperseded Status = "superseded"
)

// statuses is the allowed set in lifecycle order. The index of a status is
// also its progress, which is what the dependency rule compares.
var statuses = []Status{
	StatusDrafted,
	StatusValidated,
	StatusDispatched,
	StatusInProgress,
	StatusTesting,
	StatusComplete,
	StatusSuperseded,
}

// ArchiveDir is the subdirectory a terminal spec moves into. It keeps its
// number, so a reference to the spec resolves after the move.
const ArchiveDir = ".archive"

// IndexName is the generated index of the spec directory.
const IndexName = "README.md"

// requiredFields are the frontmatter keys every spec declares. depends_on and
// affects may be empty lists, but the keys are present, because an absent key
// and an empty list are different statements and only one of them is a
// decision.
var requiredFields = []string{"title", "status", "depends_on", "affects", "created", "author", "trigger"}

// valueFields are the keys whose value must not be empty.
var valueFields = []string{"title", "status", "created", "author", "trigger"}

// sections is the fixed body order. A spec that opens with its solution hides
// the reasoning that justifies it, so Problem comes first and the validator
// keeps it there.
var sections = []string{"Problem", "Scope", "Design", "Acceptance criteria"}

// OutcomeSection records what shipped and where it diverged from the design.
// A complete spec without one describes an intention, not the code.
const OutcomeSection = "Outcome"

// nameRE matches a spec file name: a stable number, a dash, and a slug.
var nameRE = regexp.MustCompile(`^(\d{3,})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)

// headingRE matches a level-two heading and captures its text.
var headingRE = regexp.MustCompile(`^##\s+(.+?)\s*$`)

// Spec is one parsed spec file.
type Spec struct {
	// Path is the path the spec was read from, relative to the working
	// directory, with forward slashes.
	Path string
	// Name is the file name.
	Name string
	// Number is the stable identifier from the file name.
	Number int
	// Archived reports that the file sits in the archive subdirectory.
	Archived bool

	Title     string
	Status    Status
	DependsOn []string
	Affects   []string
	Created   string
	Author    string
	Trigger   string

	// Fields records which frontmatter keys were present, so a missing key is
	// distinguishable from an empty value.
	Fields map[string]bool
	// Headings are the level-two headings in file order.
	Headings []string
}

// HasSection reports whether the body carries the named level-two section.
func (s *Spec) HasSection(name string) bool {
	for _, h := range s.Headings {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// Load reads every spec in dir and in its archive subdirectory. The index is
// not a spec and is skipped.
func Load(dir string) ([]*Spec, error) {
	var specs []*Spec
	for _, sub := range []string{"", ArchiveDir} {
		from := filepath.Join(dir, sub)
		entries, err := os.ReadDir(from)
		if err != nil {
			if sub == ArchiveDir && os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", from, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == IndexName {
				continue
			}
			path := filepath.Join(from, e.Name())
			s, err := parseFile(path)
			if err != nil {
				return nil, err
			}
			s.Archived = sub == ArchiveDir
			specs = append(specs, s)
		}
	}
	return specs, nil
}

// parseFile reads one spec. A file that cannot be parsed at all is reported
// here; a file that parses but breaks a rule is reported by the validator, so
// one run names every rule failure instead of the first.
func parseFile(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	s := &Spec{
		Path:   filepath.ToSlash(path),
		Name:   filepath.Base(path),
		Fields: map[string]bool{},
	}
	if m := nameRE.FindStringSubmatch(s.Name); m != nil {
		n, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			return nil, fmt.Errorf("%s: unreadable spec number: %w", s.Path, convErr)
		}
		s.Number = n
	}
	front, body := split(string(data))
	if err := s.parseFrontmatter(front); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	for line := range strings.SplitSeq(body, "\n") {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			s.Headings = append(s.Headings, m[1])
		}
	}
	return s, nil
}

// split separates the frontmatter block from the body. A file with no
// frontmatter returns an empty block, and the missing-field rule reports it.
func split(text string) (front, body string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", text
	}
	rest := text[len("---\n"):]
	front, body, found := strings.Cut(rest, "\n---")
	if !found {
		return rest, ""
	}
	return front, strings.TrimPrefix(body, "\n")
}

// parseFrontmatter reads the subset of YAML a spec header uses: scalar values,
// block lists, and inline lists. The subset is deliberate. A spec header that
// needs more structure than this is carrying design detail that belongs in the
// body.
func (s *Spec) parseFrontmatter(front string) error {
	var key string
	for raw := range strings.SplitSeq(front, "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if item, ok := listItem(line); ok {
			if key == "" {
				return fmt.Errorf("list item %q with no key above it", item)
			}
			s.appendValue(key, item)
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("frontmatter line %q is not key: value", line)
		}
		key = strings.TrimSpace(name)
		s.Fields[key] = true
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if items, ok := inlineList(value); ok {
			for _, item := range items {
				s.appendValue(key, item)
			}
			continue
		}
		s.setScalar(key, unquote(value))
	}
	return nil
}

// listItem reports a block list entry and its value.
func listItem(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "- ") && trimmed != "-" {
		return "", false
	}
	return unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))), true
}

// inlineList reports the entries of a bracketed list.
func inlineList(value string) ([]string, bool) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, false
	}
	inner := strings.TrimSpace(value[1 : len(value)-1])
	if inner == "" {
		return nil, true
	}
	var items []string
	for part := range strings.SplitSeq(inner, ",") {
		if item := unquote(strings.TrimSpace(part)); item != "" {
			items = append(items, item)
		}
	}
	return items, true
}

// unquote removes one layer of matching quotes.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

func (s *Spec) setScalar(key, value string) {
	switch key {
	case "title":
		s.Title = value
	case "status":
		s.Status = Status(value)
	case "created":
		s.Created = value
	case "author":
		s.Author = value
	case "trigger":
		s.Trigger = value
	}
}

func (s *Spec) appendValue(key, value string) {
	switch key {
	case "depends_on":
		s.DependsOn = append(s.DependsOn, value)
	case "affects":
		s.Affects = append(s.Affects, value)
	}
}
