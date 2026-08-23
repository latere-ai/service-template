package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WorkflowDir is where a repository's workflows live.
const WorkflowDir = ".github/workflows"

// usesPattern captures the reference on a `uses:` line, with or without a
// trailing comment naming the human-readable version.
var usesPattern = regexp.MustCompile(`^\s*-?\s*uses:\s*['"]?([^'"\s#]+)['"]?`)

// digestPattern is a full commit digest. A short digest is not accepted,
// because a short prefix can be made to collide.
var digestPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// reusableWorkflowDir marks a reference to a called workflow rather than to
// an action.
const reusableWorkflowDir = "/" + WorkflowDir + "/"

// imageDigestPattern is the digest form a container reference takes.
var imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ActionRef is one action reference found in a workflow.
type ActionRef struct {
	File string
	Line int
	Ref  string
}

// FindActionRefs lists every `uses:` reference under a workflow directory.
func FindActionRefs(dir string) ([]ActionRef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var refs []ActionRef
	for _, e := range entries {
		if e.IsDir() || !isWorkflow(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			m := usesPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			refs = append(refs, ActionRef{File: path, Line: i + 1, Ref: m[1]})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		return refs[i].Line < refs[j].Line
	})
	return refs, nil
}

// isWorkflow reports whether a file name is a workflow document.
func isWorkflow(name string) bool {
	base := strings.TrimSuffix(name, ".tmpl")
	return strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")
}

// IsPinned reports whether a reference names an immutable revision. A tag on
// an action repository can be moved by its owner, which makes a tag-pinned
// action a mutable dependency inside the build.
func IsPinned(ref string) bool {
	switch {
	case strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../"):
		// A reference to a file in this repository is pinned by the commit
		// the run checked out.
		return true
	case strings.Contains(ref, reusableWorkflowDir):
		// A reusable workflow is not an action. The template contract has
		// workflows ride a moving major tag while materialized files are
		// pinned exactly, which is what lets a fix to the pipeline reach a
		// repository without a generator run.
		return true
	case strings.HasPrefix(ref, "docker://"):
		_, digest, ok := strings.Cut(strings.TrimPrefix(ref, "docker://"), "@")
		return ok && imageDigestPattern.MatchString(digest)
	}
	_, rev, ok := strings.Cut(ref, "@")
	return ok && digestPattern.MatchString(rev)
}

// CheckActionPins reports every reference that is not pinned by digest.
func CheckActionPins(dir string) ([]string, error) {
	refs, err := FindActionRefs(dir)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, ref := range refs {
		if IsPinned(ref.Ref) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s:%d: %s is pinned by tag or branch; pin it by commit digest",
			ref.File, ref.Line, ref.Ref))
	}
	return problems, nil
}
