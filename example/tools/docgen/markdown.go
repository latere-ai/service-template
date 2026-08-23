package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// skipDirs are the trees no documentation check reads: build output, vendored
// code, and dependency directories. They hold documents this repository does
// not own, and a failure in one of them is not actionable here.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"out":          true,
	"vendor":       true,
	"dist":         true,
	"testdata":     true,
}

// MarkdownFiles returns every Markdown file under root, sorted, so a report
// reads in the same order on every machine.
func MarkdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// fenceRE matches the start or end of a fenced code block.
var fenceRE = regexp.MustCompile("^\\s*(```+|~~~+)\\s*([A-Za-z0-9_+-]*)\\s*$")

// headingRE matches an ATX heading and captures its text.
var headingRE = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$`)

// anchorStrip removes the characters a heading anchor drops.
var anchorStrip = regexp.MustCompile(`[^\w\- ]`)

// Anchors returns the heading anchors of a document, in the slug form a link
// target uses: lowercase, punctuation removed, spaces replaced by dashes, and
// a numeric suffix for a repeated heading.
func Anchors(text string) map[string]bool {
	anchors := map[string]bool{}
	seen := map[string]int{}
	inFence := false
	for line := range strings.SplitSeq(text, "\n") {
		if fenceRE.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := headingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slug := Slug(m[2])
		if n := seen[slug]; n > 0 {
			anchors[slug+"-"+strconv.Itoa(n)] = true
		}
		seen[slug]++
		anchors[slug] = true
	}
	return anchors
}

// Slug renders a heading as its anchor.
func Slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	// Inline code and emphasis markers are not part of the anchor.
	s = strings.NewReplacer("`", "", "*", "", "_", "_").Replace(s)
	s = anchorStrip.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}

// readFile reads a document and normalizes its line endings, so a line number
// in a report matches what an editor shows.
func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}
