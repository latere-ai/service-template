// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PlanFile is one file the declaration selects, already rendered. A plan is a
// pure function of the skeleton and the declaration, which is what makes the
// drift check meaningful.
type PlanFile struct {
	Entry Entry
	// Target is the path in the generated repository, after the cmd directory
	// rename.
	Target string
	// Content is the whole rendered file.
	Content []byte
	// Body is the managed region of a merged file, without its markers.
	Body []string
	// Digest is what the lock records: the whole file, or the managed region
	// of a merged file.
	Digest string
}

// Plan is the rendered selection for a declaration.
type Plan struct {
	Files []PlanFile
}

// Lookup returns the plan entry for a repository path.
func (p *Plan) Lookup(target string) (PlanFile, bool) {
	for _, f := range p.Files {
		if f.Target == target {
			return f, true
		}
	}
	return PlanFile{}, false
}

// BuildPlan renders every file the declaration selects, in path order.
func BuildPlan(src fs.FS, cfg *Config) (*Plan, error) {
	m, err := LoadManifest(src)
	if err != nil {
		return nil, err
	}
	selected := m.Select(cfg)
	p := &Plan{Files: make([]PlanFile, 0, len(selected))}
	for _, e := range selected {
		content, err := Render(src, e, cfg)
		if err != nil {
			return nil, err
		}
		pf := PlanFile{Entry: e, Target: TargetPath(e.Path, cfg.Name), Content: content}
		if e.Mode == ModeMerged {
			region, err := SplitRegion("skeleton file "+e.Source, content)
			if err != nil {
				return nil, err
			}
			pf.Body = region.Body
			pf.Digest = Digest(region.Content())
		} else {
			pf.Digest = Digest(content)
		}
		p.Files = append(p.Files, pf)
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Target < p.Files[j].Target })
	return p, nil
}

// ErrProfileChange reports a declaration whose profile no longer matches the
// profile the repository was scaffolded with. A profile selects the shape of
// the repository, so changing it is a new scaffold rather than a sync.
var ErrProfileChange = errors.New("profile change")

// guardProfile refuses a profile that differs from the one in the lock.
func guardProfile(cfg *Config, lock *Lock) error {
	if lock == nil || lock.Profile == "" || lock.Profile == cfg.Profile {
		return nil
	}
	return fmt.Errorf(
		"%w: this repository was scaffolded with the %s profile and %s now says %s;"+
			" a profile change means a new scaffold, not a sync",
		ErrProfileChange, lock.Profile, ConfigFile, cfg.Profile)
}

// SyncReport records what a sync did, so that upgrade can print it.
type SyncReport struct {
	Created   []string
	Updated   []string
	Removed   []string
	Unchanged []string
	// Diffs holds a unified diff per updated path, old side first.
	Diffs map[string]string
}

func (r *SyncReport) String() string {
	var b strings.Builder
	section := func(label string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s (%d):\n", label, len(paths))
		for _, p := range paths {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	section("created", r.Created)
	section("updated", r.Updated)
	section("removed", r.Removed)
	if len(r.Created)+len(r.Updated)+len(r.Removed) == 0 {
		fmt.Fprintf(&b, "already current, %d files unchanged\n", len(r.Unchanged))
	}
	return b.String()
}

// Sync writes every selected file, removes what the declaration deselected,
// and rewrites the lock. A seed file already recorded in the lock is left
// alone, because a seed is written once.
func Sync(src fs.FS, dir string, cfg *Config, lock *Lock) (*SyncReport, error) {
	if err := guardProfile(cfg, lock); err != nil {
		return nil, err
	}
	plan, err := BuildPlan(src, cfg)
	if err != nil {
		return nil, err
	}
	report := &SyncReport{Diffs: map[string]string{}}
	next := &Lock{
		Version:  cfg.Version,
		Profile:  cfg.Profile,
		Features: map[string]bool{},
	}
	for _, f := range AllFeatures {
		next.Features[f] = cfg.Features[f]
	}
	selected := map[string]bool{}
	for _, pf := range plan.Files {
		selected[pf.Target] = true
		entry, err := syncFile(dir, pf, lock, report)
		if err != nil {
			return nil, err
		}
		next.Files = append(next.Files, entry)
	}
	removed, err := removeDeselected(dir, lock, selected)
	if err != nil {
		return nil, err
	}
	report.Removed = removed
	if err := WriteLock(dir, next); err != nil {
		return nil, err
	}
	sort.Strings(report.Created)
	sort.Strings(report.Updated)
	sort.Strings(report.Unchanged)
	return report, nil
}

func syncFile(dir string, pf PlanFile, lock *Lock, report *SyncReport) (LockEntry, error) {
	full := filepath.Join(dir, filepath.FromSlash(pf.Target))
	disk, readErr := os.ReadFile(full)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return LockEntry{}, fmt.Errorf("read %s: %w", pf.Target, readErr)
	}
	entry := LockEntry{Path: pf.Target, Mode: pf.Entry.Mode, Digest: pf.Digest}

	if pf.Entry.Mode == ModeSeed {
		if _, recorded := lock.Entry(pf.Target); recorded {
			report.Unchanged = append(report.Unchanged, pf.Target)
			return entry, nil
		}
		if exists {
			// The repository already holds the file. A seed is consumer text
			// after the first write, so it is recorded and not overwritten.
			entry.Digest = Digest(disk)
			report.Unchanged = append(report.Unchanged, pf.Target)
			return entry, nil
		}
		if err := writeFile(full, pf.Content); err != nil {
			return LockEntry{}, err
		}
		report.Created = append(report.Created, pf.Target)
		return entry, nil
	}

	want := pf.Content
	if pf.Entry.Mode == ModeMerged && exists {
		spliced, err := Splice(pf.Target, disk, pf.Body)
		if err != nil {
			return LockEntry{}, err
		}
		want = spliced
	}
	switch {
	case !exists:
		if err := writeFile(full, want); err != nil {
			return LockEntry{}, err
		}
		report.Created = append(report.Created, pf.Target)
	case string(disk) == string(want):
		report.Unchanged = append(report.Unchanged, pf.Target)
	default:
		if err := writeFile(full, want); err != nil {
			return LockEntry{}, err
		}
		report.Updated = append(report.Updated, pf.Target)
		report.Diffs[pf.Target] = UnifiedDiff(pf.Target, disk, want)
	}
	return entry, nil
}

// removeDeselected drops files the lock records and the declaration no longer
// selects. A generated file is deleted, a merged file loses its managed region
// and keeps the consumer text, and a seed file is left because the consumer
// owns it.
func removeDeselected(dir string, lock *Lock, selected map[string]bool) ([]string, error) {
	var removed []string
	for _, e := range lock.Files {
		if selected[e.Path] {
			continue
		}
		full := filepath.Join(dir, filepath.FromSlash(e.Path))
		switch e.Mode {
		case ModeSeed:
			continue
		case ModeMerged:
			disk, err := os.ReadFile(full)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", e.Path, err)
			}
			stripped, err := StripRegion(e.Path, disk)
			if err != nil {
				return nil, err
			}
			if err := writeFile(full, stripped); err != nil {
				return nil, err
			}
			removed = append(removed, e.Path)
		default:
			err := os.Remove(full)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("remove %s: %w", e.Path, err)
			}
			removed = append(removed, e.Path)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func writeFile(full string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", full, err)
	}
	mode := os.FileMode(0o644)
	if isExecutablePath(full) {
		mode = 0o755
	}
	if err := os.WriteFile(full, content, mode); err != nil {
		return fmt.Errorf("write %s: %w", full, err)
	}
	return os.Chmod(full, mode)
}

// isExecutablePath reports whether a generated file must be executable. Git
// hooks and repository scripts do not run otherwise.
func isExecutablePath(full string) bool {
	p := filepath.ToSlash(full)
	if strings.Contains(p, "/.githooks/") {
		return true
	}
	if strings.Contains(p, "/scripts/") && strings.HasSuffix(p, ".sh") {
		return true
	}
	return false
}
