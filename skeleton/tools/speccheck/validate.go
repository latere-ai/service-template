package main

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// Validate applies every rule to the loaded specs and returns one problem per
// rule failure, sorted by the file it belongs to. Every rule runs, because a
// validator that stops at the first failure turns one review into a queue of
// single-fix runs.
func Validate(specs []*Spec) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	byName := map[string][]*Spec{}
	byNumber := map[int][]*Spec{}
	for _, s := range specs {
		byName[s.Name] = append(byName[s.Name], s)
		if s.Number != 0 {
			byNumber[s.Number] = append(byNumber[s.Number], s)
		}
	}

	for _, s := range specs {
		problems = append(problems, checkName(s)...)
		problems = append(problems, checkFrontmatter(s)...)
		problems = append(problems, checkBody(s)...)
		problems = append(problems, checkDependencies(s, byName, byNumber)...)
	}

	for number, group := range byNumber {
		if len(group) > 1 {
			add("%s: number %03d is used by %s; numbers are stable identifiers and are never reused",
				group[0].Path, number, strings.Join(names(group), ", "))
		}
	}

	problems = append(problems, checkCycles(specs, byName, byNumber)...)
	sort.Strings(problems)
	return problems
}

// checkName applies the file name rule. The number in the name is the
// identifier every reference resolves through, so a name it cannot be read
// from is a failure of the directory and not of one file.
func checkName(s *Spec) []string {
	if s.Number == 0 {
		return []string{fmt.Sprintf("%s: file name is not NNN-name.md", s.Path)}
	}
	return nil
}

// checkFrontmatter applies the required-field and allowed-status rules.
func checkFrontmatter(s *Spec) []string {
	var problems []string
	for _, field := range requiredFields {
		if !s.Fields[field] {
			problems = append(problems, fmt.Sprintf("%s: frontmatter has no %s field", s.Path, field))
		}
	}
	for _, field := range valueFields {
		if s.Fields[field] && scalar(s, field) == "" {
			problems = append(problems, fmt.Sprintf("%s: frontmatter field %s is empty", s.Path, field))
		}
	}
	if s.Status != "" && !slices.Contains(statuses, s.Status) {
		problems = append(problems, fmt.Sprintf("%s: status %q is not one of %s",
			s.Path, s.Status, statusList()))
	}
	return problems
}

// scalar reads a scalar frontmatter field by name.
func scalar(s *Spec, field string) string {
	switch field {
	case "title":
		return s.Title
	case "status":
		return string(s.Status)
	case "created":
		return s.Created
	case "author":
		return s.Author
	case "trigger":
		return s.Trigger
	}
	return ""
}

// checkBody applies the section rules: the fixed order, and the Outcome a
// complete spec records.
func checkBody(s *Spec) []string {
	var problems []string
	if s.Status == StatusComplete && !s.HasSection(OutcomeSection) {
		problems = append(problems, fmt.Sprintf(
			"%s: status is complete with no %s section; a complete spec records what shipped and where it diverged",
			s.Path, OutcomeSection))
	}
	if s.Status == StatusSuperseded {
		return problems
	}
	at := -1
	for _, want := range sections {
		found := -1
		for i, h := range s.Headings {
			if strings.EqualFold(h, want) {
				found = i
				break
			}
		}
		switch {
		case found < 0:
			problems = append(problems, fmt.Sprintf("%s: body has no %s section", s.Path, want))
		case found < at:
			problems = append(problems, fmt.Sprintf(
				"%s: section %s comes after %s; the order is %s",
				s.Path, want, s.Headings[at], strings.Join(sections, ", ")))
		default:
			at = found
		}
	}
	return problems
}

// checkDependencies applies the reference and readiness rules: every
// depends_on target resolves, and a spec that is dispatched or further along
// has no dependency that is still open.
func checkDependencies(s *Spec, byName map[string][]*Spec, byNumber map[int][]*Spec) []string {
	var problems []string
	for _, ref := range s.DependsOn {
		dep, ok := resolve(ref, byName, byNumber)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: depends_on %s does not resolve to a spec", s.Path, ref))
			continue
		}
		if dispatchedOrLater(s.Status) && !settled(dep.Status) {
			problems = append(problems, fmt.Sprintf(
				"%s: status is %s while its dependency %s is %s; work starts when its dependencies are complete",
				s.Path, s.Status, dep.Path, dep.Status))
		}
	}
	return problems
}

// resolve finds the spec a depends_on entry names. The entry is matched by
// file name first and by number second, so archiving a spec, which moves the
// file and keeps the number, leaves every inbound reference resolvable.
func resolve(ref string, byName map[string][]*Spec, byNumber map[int][]*Spec) (*Spec, bool) {
	name := path.Base(strings.TrimSpace(strings.ReplaceAll(ref, "\\", "/")))
	if group, ok := byName[name]; ok && len(group) > 0 {
		return group[0], true
	}
	if m := nameRE.FindStringSubmatch(name); m != nil {
		var number int
		if _, err := fmt.Sscanf(m[1], "%d", &number); err == nil {
			if group, ok := byNumber[number]; ok && len(group) > 0 {
				return group[0], true
			}
		}
	}
	return nil, false
}

// dispatchedOrLater reports a status at or beyond the point where work starts.
func dispatchedOrLater(s Status) bool {
	switch s {
	case StatusDispatched, StatusInProgress, StatusTesting, StatusComplete:
		return true
	default:
		return false
	}
}

// settled reports a dependency that no longer blocks. A superseded spec
// settles because its work moved to the spec that replaced it, which carries
// its own dependency edges.
func settled(s Status) bool { return s == StatusComplete || s == StatusSuperseded }

// checkCycles reports every dependency cycle and names the specs in it. A
// cycle means no spec in the loop can ever start, which is invisible in a
// pairwise reading of the files.
func checkCycles(specs []*Spec, byName map[string][]*Spec, byNumber map[int][]*Spec) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[*Spec]int{}
	var stack []*Spec
	reported := map[string]bool{}
	var problems []string

	ordered := append([]*Spec(nil), specs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })

	var visit func(s *Spec)
	visit = func(s *Spec) {
		color[s] = grey
		stack = append(stack, s)
		for _, ref := range s.DependsOn {
			dep, ok := resolve(ref, byName, byNumber)
			if !ok {
				continue
			}
			switch color[dep] {
			case white:
				visit(dep)
			case grey:
				if cycle := cycleFrom(stack, dep); cycle != "" && !reported[cycle] {
					reported[cycle] = true
					problems = append(problems, fmt.Sprintf("dependency cycle: %s", cycle))
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[s] = black
	}
	for _, s := range ordered {
		if color[s] == white {
			visit(s)
		}
	}
	return problems
}

// cycleFrom renders the loop on the stack starting at the spec that closed it.
func cycleFrom(stack []*Spec, at *Spec) string {
	start := slices.Index(stack, at)
	if start < 0 {
		return ""
	}
	var parts []string
	for _, s := range stack[start:] {
		parts = append(parts, s.Name)
	}
	return strings.Join(append(parts, at.Name), " -> ")
}

// names renders the paths of a group of specs.
func names(group []*Spec) []string {
	var out []string
	for _, s := range group {
		out = append(out, s.Path)
	}
	sort.Strings(out)
	return out
}

// statusList renders the allowed statuses for a failure message.
func statusList() string {
	var out []string
	for _, s := range statuses {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}
