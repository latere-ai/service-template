package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Lock is the record the generator writes beside the declaration. It answers
// the question the drift check needs and the declaration cannot: what the
// generator last wrote. Without it an edited file and an upstream change are
// the same observation.
//
// The lock also records the profile and the feature set, because a profile is
// fixed at scaffold time and the only durable evidence of the profile a
// repository was scaffolded with is the lock.
type Lock struct {
	Version  string
	Profile  string
	Features map[string]bool
	Files    []LockEntry
}

// LockEntry is one file the generator wrote. For a merged file the digest
// covers the managed region only, because the consumer owns the rest.
type LockEntry struct {
	Path   string
	Mode   Mode
	Digest string
}

// Digest is the content digest recorded in the lock and compared by the check.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Entry returns the lock record for a generated path.
func (l *Lock) Entry(path string) (LockEntry, bool) {
	if l == nil {
		return LockEntry{}, false
	}
	for _, e := range l.Files {
		if e.Path == path {
			return e, true
		}
	}
	return LockEntry{}, false
}

// LoadLock reads dir/template.lock. A repository with no lock yet yields an
// empty lock rather than an error, so the first sync in a hand-written
// repository behaves like a fresh scaffold.
func LoadLock(dir string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(dir, LockFile))
	if os.IsNotExist(err) {
		return &Lock{Features: map[string]bool{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", LockFile, err)
	}
	return ParseLock(LockFile, data)
}

// ParseLock parses a lock document. The file name appears in error messages.
func ParseLock(file string, data []byte) (*Lock, error) {
	root, err := parseYAML(file, data)
	if err != nil {
		return nil, err
	}
	if root.kind != kindMapping {
		return nil, errAt(file, 1, "the lock must be a mapping of fields")
	}
	l := &Lock{Features: map[string]bool{}}
	for _, k := range root.keys {
		switch k {
		case "version", "profile", "features", "files":
		default:
			return nil, errAt(file, root.get(k).line, "unknown field %q", k)
		}
	}
	if l.Version, err = root.scalar(file, "version"); err != nil {
		return nil, err
	}
	if l.Profile, err = root.scalar(file, "profile"); err != nil {
		return nil, err
	}
	if f := root.get("features"); f != nil {
		if f.kind != kindMapping {
			return nil, errAt(file, f.line, "\"features\" must be a mapping of flag names to true or false")
		}
		for _, k := range f.keys {
			v, err := f.boolean(file, k)
			if err != nil {
				return nil, err
			}
			l.Features[k] = v
		}
	}
	files := root.get("files")
	if files == nil {
		return l, nil
	}
	if files.kind != kindSequence {
		return nil, errAt(file, files.line, "\"files\" must be a list")
	}
	seen := map[string]bool{}
	for _, item := range files.items {
		if item.kind != kindMapping {
			return nil, errAt(file, item.line, "each lock entry must hold path, mode, and digest")
		}
		p, err := item.scalar(file, "path")
		if err != nil {
			return nil, err
		}
		modeText, err := item.scalar(file, "mode")
		if err != nil {
			return nil, err
		}
		digest, err := item.scalar(file, "digest")
		if err != nil {
			return nil, err
		}
		if p == "" {
			return nil, errAt(file, item.line, "a lock entry needs a path")
		}
		if seen[p] {
			return nil, errAt(file, item.line, "duplicate lock entry for %q", p)
		}
		seen[p] = true
		switch Mode(modeText) {
		case ModeGenerated, ModeSeed, ModeMerged:
		default:
			return nil, errAt(file, item.line, "the lock entry for %q has mode %q", p, modeText)
		}
		l.Files = append(l.Files, LockEntry{Path: p, Mode: Mode(modeText), Digest: digest})
	}
	sort.Slice(l.Files, func(i, j int) bool { return l.Files[i].Path < l.Files[j].Path })
	return l, nil
}

// Marshal renders the lock in a fixed order so that two syncs with the same
// inputs produce byte-identical files.
func (l *Lock) Marshal() []byte {
	var b strings.Builder
	b.WriteString("# Written by the template generator. Do not edit.\n")
	fmt.Fprintf(&b, "version: %s\n", l.Version)
	fmt.Fprintf(&b, "profile: %s\n", l.Profile)
	b.WriteString("features:\n")
	for _, f := range AllFeatures {
		fmt.Fprintf(&b, "  %s: %t\n", f, l.Features[f])
	}
	files := append([]LockEntry(nil), l.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) == 0 {
		b.WriteString("files: []\n")
		return []byte(b.String())
	}
	b.WriteString("files:\n")
	for _, e := range files {
		fmt.Fprintf(&b, "  - path: %s\n", e.Path)
		fmt.Fprintf(&b, "    mode: %s\n", e.Mode)
		fmt.Fprintf(&b, "    digest: %s\n", e.Digest)
	}
	return []byte(b.String())
}

// WriteLock writes the lock to dir.
func WriteLock(dir string, l *Lock) error {
	if err := os.WriteFile(filepath.Join(dir, LockFile), l.Marshal(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", LockFile, err)
	}
	return nil
}
