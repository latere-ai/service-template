package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PublishedDocs are the documents written for a reader outside the team. They
// are read by people who cannot resolve an internal reference, so a citation
// of a tracker item or of a spec identifier is a dead end for most of the
// audience.
var PublishedDocs = []string{"README.md", "CONTRIBUTING.md", "SECURITY.md", "docs"}

// internalPatterns are the references a published document may not carry. The
// set is deliberately narrow: an identifier that only resolves inside the team,
// not every hyphenated token. A rule that fires on "UTF-8" is a rule people
// switch off.
var internalPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a spec file identifier", regexp.MustCompile(`(?i)\bspecs?/[0-9]{3,}[-\w]*\.md\b`)},
	{"a spec number", regexp.MustCompile(`(?i)\bspec[ -]?[0-9]{3,}\b`)},
	{"an issue tracker link", regexp.MustCompile(`(?i)https?://[^\s)]*(atlassian\.net|jira\.|linear\.app|notion\.so|confluence)`)},
	{"a work item identifier", regexp.MustCompile(`(?i)\b(jira|ticket|work item)[ -]?[0-9]+\b`)},
}

// CheckReferences reports internal references in the published documents under
// root.
func CheckReferences(root string) []string {
	var problems []string
	for _, entry := range PublishedDocs {
		path := filepath.Join(root, entry)
		for _, file := range documentsAt(path) {
			text, err := readFile(file)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", file, err))
				continue
			}
			problems = append(problems, referencesIn(file, text)...)
		}
	}
	return problems
}

// documentsAt resolves a published entry to the files it names. A missing
// entry is not a failure of this check: whether a document must exist is the
// documentation set's rule, and it is checked there.
func documentsAt(path string) []string {
	if strings.EqualFold(filepath.Ext(path), ".md") {
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		return []string{filepath.ToSlash(path)}
	}
	files, err := MarkdownFiles(path)
	if err != nil {
		return nil
	}
	return files
}

// referencesIn reports the internal references in one document.
func referencesIn(file, text string) []string {
	var problems []string
	for i, line := range strings.Split(text, "\n") {
		for _, p := range internalPatterns {
			if m := p.re.FindString(line); m != "" {
				problems = append(problems, fmt.Sprintf(
					"%s:%d: %s is %s; describe the reason in the reader's terms instead",
					file, i+1, m, p.name))
			}
		}
	}
	return problems
}
