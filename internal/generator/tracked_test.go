package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest entry names a file the generator will copy into every scaffold, so
// the file has to be in the repository, not merely on the machine that wrote
// the manifest. An ignore rule can hide one: `coverage/` without a leading
// slash matches a directory of that name at any depth and silently swallows a
// committed fixture. The result builds locally and fails on a clean checkout,
// which is the worst place to find out.
func TestEveryDeclaredSkeletonFileIsTracked(t *testing.T) {
	root := "../.."
	m, err := LoadManifest(os.DirFS(filepath.Join(root, "skeleton")))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	tracked := trackedFiles(t, root)
	var missing []string
	for _, e := range m.Entries {
		if e.Source == "" {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("skeleton", e.Source))
		if !tracked[rel] {
			missing = append(missing, rel+"  (declared in "+e.Fragment+")")
		}
	}
	if len(missing) > 0 {
		t.Errorf("declared skeleton files are not tracked by git, so a fresh clone cannot generate:\n  %s\n"+
			"check .gitignore for an unanchored pattern", strings.Join(missing, "\n  "))
	}
}

func trackedFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-files", "skeleton")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			set[line] = true
		}
	}
	return set
}
