package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// linkRE matches an inline Markdown link and a reference definition. Both
// carry a target this check resolves.
var (
	inlineLinkRE = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	refLinkRE    = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*(\S+)`)
)

// Link is one link found in a document.
type Link struct {
	File   string
	Line   int
	Target string
}

// CollectLinks returns every link in the Markdown files under root. Links
// inside a fenced code block are examples rather than references and are
// skipped.
func CollectLinks(root string) ([]Link, error) {
	files, err := MarkdownFiles(root)
	if err != nil {
		return nil, err
	}
	var links []Link
	for _, file := range files {
		text, err := readFile(file)
		if err != nil {
			return nil, err
		}
		inFence := false
		for i, line := range strings.Split(text, "\n") {
			if fenceRE.MatchString(line) {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			for _, m := range inlineLinkRE.FindAllStringSubmatch(line, -1) {
				links = append(links, Link{File: file, Line: i + 1, Target: m[1]})
			}
			if m := refLinkRE.FindStringSubmatch(line); m != nil {
				links = append(links, Link{File: file, Line: i + 1, Target: m[1]})
			}
		}
	}
	return links, nil
}

// CheckLinks resolves every internal link under root and reports the ones that
// do not resolve. External links are checked only when external is true,
// because a documentation gate that needs the network fails for reasons that
// have nothing to do with the change under test.
func CheckLinks(ctx context.Context, root string, external bool, client *http.Client) []string {
	links, err := CollectLinks(root)
	if err != nil {
		return []string{fmt.Sprintf("read the documents: %v", err)}
	}

	anchors := map[string]map[string]bool{}
	anchorsOf := func(file string) map[string]bool {
		if a, ok := anchors[file]; ok {
			return a
		}
		text, err := readFile(file)
		if err != nil {
			anchors[file] = nil
			return nil
		}
		a := Anchors(text)
		anchors[file] = a
		return a
	}

	var problems []string
	for _, l := range links {
		target := l.Target
		switch {
		case strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "tel:"):
			continue
		case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
			if !external {
				continue
			}
			if err := reachable(ctx, client, target); err != nil {
				problems = append(problems, fmt.Sprintf("%s:%d: %s: %v", l.File, l.Line, target, err))
			}
			continue
		case strings.HasPrefix(target, "#"):
			if a := anchorsOf(l.File); a != nil && !a[strings.TrimPrefix(target, "#")] {
				problems = append(problems, fmt.Sprintf("%s:%d: no heading matches %s", l.File, l.Line, target))
			}
			continue
		}

		file, fragment, _ := strings.Cut(target, "#")
		if file == "" {
			continue
		}
		resolved := filepath.FromSlash(path.Join(path.Dir(filepath.ToSlash(l.File)), file))
		info, err := os.Stat(resolved)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: %s does not exist", l.File, l.Line, target))
			continue
		}
		if fragment == "" || info.IsDir() || !strings.EqualFold(filepath.Ext(resolved), ".md") {
			continue
		}
		if a := anchorsOf(filepath.ToSlash(resolved)); a != nil && !a[fragment] {
			problems = append(problems, fmt.Sprintf("%s:%d: %s has no heading %s", l.File, l.Line, file, fragment))
		}
	}
	return problems
}

// reachable reports whether an external target answers. A HEAD that is refused
// is retried as a GET, because some hosts answer only the second.
func reachable(ctx context.Context, client *http.Client, target string) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			return nil
		}
		if method == http.MethodGet {
			return fmt.Errorf("answered %d", resp.StatusCode)
		}
	}
	return nil
}
