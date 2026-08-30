package generator

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

// ManifestDir holds one YAML fragment per content group inside the skeleton.
// Fragments are merged into one manifest so that a group can be added without
// editing a central list.
const ManifestDir = "manifests"

// TemplateSuffix marks a skeleton file that is rendered with text/template and
// loses the suffix on the way out.
const TemplateSuffix = ".tmpl"

// Mode decides what the generator does with a file after the first write.
type Mode string

const (
	// ModeGenerated files are rewritten by sync and drift is an error.
	ModeGenerated Mode = "generated"
	// ModeSeed files are written once at init and never checked again.
	ModeSeed Mode = "seed"
	// ModeMerged files carry a managed region; the consumer owns the rest.
	ModeMerged Mode = "merged"
)

// Entry is one declared skeleton file.
type Entry struct {
	// Path is the file path relative to the generated repository root, written
	// in skeleton form: the literal service name is still "service" and the
	// .tmpl suffix is omitted.
	Path string
	Mode Mode
	// Profiles limits the entry to those profiles. Empty means every profile.
	Profiles []string
	// Features lists the flags that must all be on. Empty means no flag is
	// required.
	Features []string
	// Source is the path inside the skeleton tree, including any .tmpl suffix.
	Source string
	// Fragment is the manifest fragment that declared the entry.
	Fragment string
	line     int
}

// Manifest is the merged declaration of every skeleton file.
type Manifest struct {
	Entries []Entry
}

// LoadManifest reads every fragment under manifests/ in the skeleton tree,
// merges them, and fails when two fragments declare the same path.
func LoadManifest(src fs.FS) (*Manifest, error) {
	names, err := fs.Glob(src, path.Join(ManifestDir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifest fragments: %w", err)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no manifest fragments found under %s/", ManifestDir)
	}
	m := &Manifest{}
	seen := map[string]Entry{}
	for _, name := range names {
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		entries, err := parseFragment(name, data)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if prev, dup := seen[e.Path]; dup {
				return nil, errAt(name, e.line,
					"duplicate path %q, already declared in %s:%d", e.Path, prev.Fragment, prev.line)
			}
			seen[e.Path] = e
			m.Entries = append(m.Entries, e)
		}
	}
	for i := range m.Entries {
		m.Entries[i].Source = resolveSource(src, m.Entries[i].Path)
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	return m, nil
}

// resolveSource finds the skeleton file behind a declared path. A declared
// path is written without the template suffix, so both spellings are tried.
// An entry with no file behind it keeps an empty source and is reported by
// VerifyCoverage.
func resolveSource(src fs.FS, p string) string {
	for _, candidate := range []string{p, p + TemplateSuffix} {
		if st, err := fs.Stat(src, candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func parseFragment(name string, data []byte) ([]Entry, error) {
	root, err := parseYAML(name, data)
	if err != nil {
		return nil, err
	}
	if root.kind != kindMapping {
		return nil, errAt(name, 1, "a manifest fragment must be a mapping with a \"files\" list")
	}
	for _, k := range root.keys {
		if k != "files" {
			return nil, errAt(name, root.get(k).line, "unknown field %q, a fragment holds only \"files\"", k)
		}
	}
	files := root.get("files")
	if files == nil {
		return nil, errAt(name, 1, "a manifest fragment must declare a \"files\" list")
	}
	if files.kind != kindSequence {
		return nil, errAt(name, files.line, "\"files\" must be a list")
	}
	out := make([]Entry, 0, len(files.items))
	for _, item := range files.items {
		if item.kind != kindMapping {
			return nil, errAt(name, item.line, "each file entry must hold at least a path and a mode")
		}
		for _, k := range item.keys {
			switch k {
			case "path", "mode", "profiles", "features":
			default:
				return nil, errAt(name, item.get(k).line, "unknown file field %q", k)
			}
		}
		p, err := item.scalar(name, "path")
		if err != nil {
			return nil, err
		}
		modeText, err := item.scalar(name, "mode")
		if err != nil {
			return nil, err
		}
		profiles, err := item.strings(name, "profiles")
		if err != nil {
			return nil, err
		}
		features, err := item.strings(name, "features")
		if err != nil {
			return nil, err
		}
		e := Entry{
			Path:     path.Clean(p),
			Mode:     Mode(modeText),
			Profiles: profiles,
			Features: features,
			Fragment: name,
			line:     item.line,
		}
		if err := validateEntry(name, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func validateEntry(name string, e *Entry) error {
	if e.Path == "" || e.Path == "." {
		return errAt(name, e.line, "a file entry needs a path")
	}
	if path.IsAbs(e.Path) || strings.HasPrefix(e.Path, "../") {
		return errAt(name, e.line, "the path %q must be relative to the repository root", e.Path)
	}
	if strings.HasSuffix(e.Path, TemplateSuffix) {
		return errAt(name, e.line,
			"declare %q without the %s suffix, the rendered file loses it", e.Path, TemplateSuffix)
	}
	if e.Path == ConfigFile || e.Path == LockFile {
		return errAt(name, e.line, "%q is written by the generator and must not be declared", e.Path)
	}
	if strings.HasPrefix(e.Path, ManifestDir+"/") {
		return errAt(name, e.line, "manifest fragments are not materialized")
	}
	switch e.Mode {
	case ModeGenerated, ModeSeed, ModeMerged:
	case "":
		return errAt(name, e.line, "the entry for %q needs a mode", e.Path)
	default:
		return errAt(name, e.line,
			"the entry for %q has mode %q, the modes are generated, seed, merged", e.Path, e.Mode)
	}
	for _, p := range e.Profiles {
		if _, ok := profileFeatures[p]; !ok {
			return errAt(name, e.line, "the entry for %q names unknown profile %q", e.Path, p)
		}
	}
	known := map[string]bool{}
	for _, f := range AllFeatures {
		known[f] = true
	}
	for _, f := range e.Features {
		if !known[f] {
			return errAt(name, e.line, "the entry for %q names unknown feature flag %q", e.Path, f)
		}
	}
	return nil
}

// Selects reports whether an entry belongs in a repository with this
// declaration.
func (e Entry) Selects(cfg *Config) bool {
	if len(e.Profiles) > 0 {
		match := slices.Contains(e.Profiles, cfg.Profile)
		if !match {
			return false
		}
	}
	for _, f := range e.Features {
		if !cfg.Features[f] {
			return false
		}
	}
	return true
}

// Select returns the entries a declaration selects, ordered by path.
func (m *Manifest) Select(cfg *Config) []Entry {
	out := make([]Entry, 0, len(m.Entries))
	for _, e := range m.Entries {
		if e.Selects(cfg) {
			out = append(out, e)
		}
	}
	return out
}

// Lookup returns the entry for a skeleton-form path.
func (m *Manifest) Lookup(p string) (Entry, bool) {
	for _, e := range m.Entries {
		if e.Path == p {
			return e, true
		}
	}
	return Entry{}, false
}

// skipDirs names directories that hold build output or version control state
// rather than template content. They are matched by base name because a
// frontend nests its own node_modules and dist under the frontend directory.
//
// A directory is skipped only when its name can never be template source. A
// name that is both plausible build output and plausible source, "coverage"
// among them, is not in this set: guessing wrong here drops a shipped file
// silently, and the file stops reaching consumers with nothing to show for it.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"out":          true,
	"dist":         true,
	"tmp":          true,
}

// buildOutputDirs names directories the skeleton declares files in and a local
// build also writes into. An undeclared file under one of them is build output
// rather than a file the manifest forgot.
//
// The directory itself is not skipped, because the declared files inside it
// still have to be found. The frontend build copies its bundle into the
// directory the binary embeds, which holds one committed placeholder document
// and, after a build, everything the bundler emitted.
var buildOutputDirs = []string{"internal/web/public"}

// isBuildOutput reports whether an undeclared path is build output rather than
// an undeclared template file.
func isBuildOutput(p string) bool {
	for _, dir := range buildOutputDirs {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// skipFiles names artifacts a working tree carries and the skeleton never
// ships. Without them a local build makes the coverage check fail on files
// that are not template content.
var skipFiles = map[string]bool{
	".DS_Store":     true,
	"coverage.out":  true,
	"coverage.html": true,
}

// VerifyCoverage reports skeleton files that no fragment declares, and
// declared paths with no file behind them. An undeclared file is a file the
// generator would silently drop, which is how a fix stops propagating.
func VerifyCoverage(src fs.FS) error {
	m, err := LoadManifest(src)
	if err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, e := range m.Entries {
		declared[e.Path] = true
	}
	var undeclared []string
	present := map[string]bool{}
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == ManifestDir || skipDirs[path.Base(p)] {
				return fs.SkipDir
			}
			return nil
		}
		if skipFiles[path.Base(p)] {
			return nil
		}
		out := strings.TrimSuffix(p, TemplateSuffix)
		present[out] = true
		if !declared[out] && !isBuildOutput(out) {
			undeclared = append(undeclared, p)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk the skeleton: %w", err)
	}
	var missing []string
	for _, e := range m.Entries {
		if !present[e.Path] {
			missing = append(missing, fmt.Sprintf("%s (declared in %s:%d)", e.Path, e.Fragment, e.line))
		}
	}
	sort.Strings(undeclared)
	sort.Strings(missing)
	var problems []string
	if len(undeclared) > 0 {
		problems = append(problems, "skeleton files that no manifest fragment declares:\n  "+
			strings.Join(undeclared, "\n  "))
	}
	if len(missing) > 0 {
		problems = append(problems, "declared paths with no skeleton file:\n  "+
			strings.Join(missing, "\n  "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}
