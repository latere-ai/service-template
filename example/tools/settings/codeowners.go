package main

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
)

// OwnedPath is one rule in the ownership file.
type OwnedPath struct {
	Pattern string
	Owners  []string
	Line    int
}

// LoadCodeOwners reads the ownership rules. A rule with a pattern and no owner
// is not an error in the file format but is useless here, so it is kept and
// reported by the coverage check.
func LoadCodeOwners(path string) ([]OwnedPath, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// The file is read only, so nothing is lost by a failed close.
	defer func() { _ = file.Close() }()

	var rules []OwnedPath
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		rules = append(rules, OwnedPath{Pattern: fields[0], Owners: fields[1:], Line: line})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return rules, nil
}

// VerifyCoverage reports the required paths that no rule owns.
//
// The check is about coverage and not about identity: who owns the pipeline is
// the repository's decision, but that somebody owns it is the template's, since
// a repository whose gates can be changed without an owner review has none.
func VerifyCoverage(rules []OwnedPath, required []string) error {
	var missing []string
	for _, path := range required {
		if !covered(rules, path) {
			missing = append(missing, path)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("no owner for %s; an owner review is what stops the gates from being "+
		"changed by the review they guard", strings.Join(missing, ", "))
}

// covered reports whether a rule with at least one owner matches the path. A
// rule ending in a slash owns everything below it.
func covered(rules []OwnedPath, path string) bool {
	for _, rule := range rules {
		if len(rule.Owners) == 0 {
			continue
		}
		switch {
		case rule.Pattern == path:
			return true
		case strings.HasSuffix(rule.Pattern, "/") && strings.HasPrefix(path, rule.Pattern):
			return true
		}
	}
	return false
}

// PlaceholderOwners reports the rules that still name the scaffold placeholder,
// so a repository learns during setup rather than during a review that its
// owners were never filled in.
func PlaceholderOwners(rules []OwnedPath) []OwnedPath {
	var found []OwnedPath
	for _, rule := range rules {
		if slices.Contains(rule.Owners, "@OWNER") {
			found = append(found, rule)
		}
	}
	return found
}
