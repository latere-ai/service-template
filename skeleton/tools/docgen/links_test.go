package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDocs lays out a documentation tree and returns its root.
func writeDocs(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestABrokenInternalLinkFails(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md":            "# Title\n\nSee [the guide](docs/guide.md).\n",
		"docs/architecture.md": "# Architecture\n",
	})
	problems := CheckLinks(t.Context(), dir, false, nil)
	if len(problems) != 1 {
		t.Fatalf("want one problem, got %v", problems)
	}
	if !strings.Contains(problems[0], "README.md:3") || !strings.Contains(problems[0], "docs/guide.md") {
		t.Errorf("the failure does not name the file, the line, and the target: %v", problems)
	}
}

func TestAResolvingLinkPasses(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md":     "# Title\n\nSee [the guide](docs/guide.md#a-section).\n",
		"docs/guide.md": "# Guide\n\n## A section\n\nBack to the [README](../README.md).\n",
	})
	if problems := CheckLinks(t.Context(), dir, false, nil); len(problems) != 0 {
		t.Fatalf("a resolving link was reported: %v", problems)
	}
}

func TestAMissingAnchorFails(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md":     "# Title\n\n[gone](docs/guide.md#not-here) and [local](#title)\n",
		"docs/guide.md": "# Guide\n",
	})
	problems := CheckLinks(t.Context(), dir, false, nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "not-here") {
		t.Fatalf("want the missing anchor reported once, got %v", problems)
	}
}

func TestALinkInsideACodeBlockIsAnExampleNotAReference(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md": "# Title\n\n```md\n[example](does-not-exist.md)\n```\n",
	})
	if problems := CheckLinks(t.Context(), dir, false, nil); len(problems) != 0 {
		t.Fatalf("a link inside a code block was checked: %v", problems)
	}
}

func TestExternalLinksAreCheckedOnlyWhenAsked(t *testing.T) {
	var served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		if strings.Contains(r.URL.Path, "gone") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := writeDocs(t, map[string]string{
		"README.md": "# Title\n\n[here](" + srv.URL + "/here) and [gone](" + srv.URL + "/gone)\n",
	})

	if problems := CheckLinks(t.Context(), dir, false, nil); len(problems) != 0 || served != 0 {
		t.Fatalf("external links were checked without being asked: %v, %d requests", problems, served)
	}
	problems := CheckLinks(t.Context(), dir, true, srv.Client())
	if len(problems) != 1 || !strings.Contains(problems[0], "/gone") {
		t.Fatalf("want the unreachable target reported once, got %v", problems)
	}
}

func TestMailtoAndTelTargetsAreLeftAlone(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"SECURITY.md": "# Security\n\nWrite to [us](mailto:security@example.com).\n",
	})
	if problems := CheckLinks(t.Context(), dir, false, nil); len(problems) != 0 {
		t.Fatalf("an address was treated as a path: %v", problems)
	}
}

// The documents committed in this repository resolve, which is the case the
// check exists to keep true.
func TestCommittedLinksResolve(t *testing.T) {
	if problems := CheckLinks(t.Context(), repoRoot, false, nil); len(problems) != 0 {
		t.Fatalf("the committed documents hold broken links:\n  %s", strings.Join(problems, "\n  "))
	}
}
