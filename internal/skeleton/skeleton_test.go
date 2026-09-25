// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package skeleton

import (
	"archive/zip"
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"latere.ai/x/service-template/internal/generator"
)

// update rewrites the committed archive from the tree. `make skeleton-archive`
// sets it, and `make example-update` runs that target first.
var update = flag.Bool("update", false, "rewrite "+ArchiveName+" from ../../skeleton")

const tree = "../../skeleton"

// The command generates from the archive, and a developer edits the tree, so
// an archive that has fallen behind the tree ships a skeleton nobody reviewed
// in its current form. Contents are compared rather than archive bytes, so a
// toolchain that lays out a zip file differently does not fail the check.
func TestArchiveMatchesTheSkeleton(t *testing.T) {
	src := os.DirFS(tree)
	if *update {
		data, err := Pack(src)
		if err != nil {
			t.Fatalf("pack the skeleton: %v", err)
		}
		if err := os.WriteFile(ArchiveName, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", ArchiveName, err)
		}
		return
	}

	want, err := Files(src)
	if err != nil {
		t.Fatalf("list the skeleton: %v", err)
	}
	embedded, err := FS()
	if err != nil {
		t.Fatal(err)
	}
	have, err := entries(embedded)
	if err != nil {
		t.Fatalf("list the archive: %v", err)
	}

	var stale []string
	seen := map[string]bool{}
	for _, name := range want {
		seen[name] = true
		fromTree, err := fs.ReadFile(src, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fromArchive, ok := have[name]
		switch {
		case !ok:
			stale = append(stale, "missing from the archive: "+name)
		case !bytes.Equal(fromTree, fromArchive):
			stale = append(stale, "differs from the tree: "+name)
		}
	}
	for name := range have {
		if !seen[name] {
			stale = append(stale, "no longer in the tree: "+name)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("%s does not match the skeleton tree:\n  %s\nrun: make skeleton-archive",
			ArchiveName, strings.Join(stale, "\n  "))
	}
}

// inPlace lists the skeleton files that are templates in the tree and that
// the skeleton's own gates also read in place, from the plain file beside the
// template. .lateregate.yaml is one: a service's copy depends on its profile
// and its license, and `go tool lateregate` run inside skeleton/ reads the
// skeleton's.
var inPlace = []string{".lateregate.yaml"}

// Each plain file in inPlace is its template rendered for the skeleton
// itself: the service profile, every feature, the skeleton's module and name,
// and the default license. The two are one decision written twice, so they
// are held equal. An edit to one alone would gate the skeleton by rules no
// service receives, or ship rules the skeleton was never held to. `make
// skeleton-archive` rewrites each plain file from its template.
func TestInPlaceTwinsMatchTheirTemplates(t *testing.T) {
	src := os.DirFS(tree)
	m, err := generator.LoadManifest(src)
	if err != nil {
		t.Fatalf("load the manifest: %v", err)
	}
	features := map[string]bool{}
	for _, f := range generator.AllFeatures {
		features[f] = true
	}
	cfg := &generator.Config{
		Template: generator.DefaultTemplate,
		Version:  "v0.0.0",
		Module:   generator.SkeletonModule,
		Name:     generator.SkeletonName,
		Profile:  generator.ProfileService,
		Features: features,
	}
	entries := map[string]generator.Entry{}
	for _, e := range m.Entries {
		entries[e.Path] = e
	}
	for _, p := range inPlace {
		e, ok := entries[p]
		if !ok || !strings.HasSuffix(e.Source, generator.TemplateSuffix) {
			t.Fatalf("%s is listed as an in-place twin, but the manifest declares no template for it", p)
		}
		want, err := generator.Render(src, e, cfg)
		if err != nil {
			t.Fatalf("render %s: %v", e.Source, err)
		}
		if *update {
			if err := os.WriteFile(filepath.Join(tree, filepath.FromSlash(p)), want, 0o644); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
			continue
		}
		plain, err := fs.ReadFile(src, p)
		if err != nil {
			t.Fatalf("read the in-place %s: %v\nrun: make skeleton-archive", p, err)
		}
		if !bytes.Equal(plain, want) {
			t.Errorf("%s differs from the rendering of %s for the skeleton itself\nrun: make skeleton-archive\n%s",
				p, e.Source, generator.UnifiedDiff(p, plain, want))
		}
	}
	// A plain file beside any other template is one generation ignores and no
	// check holds to anything, so it is refused rather than left to drift.
	for _, e := range m.Entries {
		if !strings.HasSuffix(e.Source, generator.TemplateSuffix) || slices.Contains(inPlace, e.Path) {
			continue
		}
		if _, err := fs.Stat(src, e.Path); err == nil {
			t.Errorf("%s sits beside %s, and generation reads only the template; delete it or list it in inPlace",
				e.Path, e.Source)
		}
	}
}

// The archive is what an installed command generates from, so it has to be a
// complete skeleton on its own terms: every declared file present, and no file
// the manifest does not account for.
func TestTheArchiveIsACompleteSkeleton(t *testing.T) {
	embedded, err := FS()
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.VerifyCoverage(embedded); err != nil {
		t.Fatalf("the embedded skeleton is incomplete: %v", err)
	}
}

// Packing is a pure function of the tree, so regenerating the archive from an
// unchanged tree changes nothing a reviewer has to read.
func TestPackIsDeterministic(t *testing.T) {
	src := os.DirFS(tree)
	first, err := Pack(src)
	if err != nil {
		t.Fatalf("pack the skeleton: %v", err)
	}
	second, err := Pack(src)
	if err != nil {
		t.Fatalf("pack the skeleton: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two packs of the same tree differ")
	}
}

// A declared path with no file behind it would ship a manifest the command
// cannot satisfy, so packing refuses it.
func TestPackRefusesADeclaredPathWithNoFile(t *testing.T) {
	src := fstestTree(map[string]string{
		"manifests/core.yaml": "files:\n  - path: README.md\n    mode: seed\n  - path: missing.txt\n    mode: generated\n",
		"README.md":           "readme\n",
	})
	_, err := Pack(src)
	if err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("Pack accepted a declared path with no file: %v", err)
	}
}

// The archive holds the manifest and what it declares, not whatever else a
// working tree carries.
func TestPackHoldsOnlyTheDeclaredFiles(t *testing.T) {
	src := fstestTree(map[string]string{
		"manifests/core.yaml":          "files:\n  - path: README.md\n    mode: seed\n  - path: cmd/service/main.go\n    mode: seed\n",
		"README.md":                    "readme\n",
		"cmd/service/main.go.tmpl":     "package main\n",
		"frontend/node_modules/x/a.js": "installed\n",
		".golangci.yml":                "rendered\n",
	})
	data, err := Pack(src)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read the archive: %v", err)
	}
	have, err := entries(r)
	if err != nil {
		t.Fatalf("list the archive: %v", err)
	}
	want := []string{"README.md", "cmd/service/main.go.tmpl", "manifests/core.yaml"}
	if len(have) != len(want) {
		t.Fatalf("the archive holds %d files, want %d: %v", len(have), len(want), have)
	}
	for _, name := range want {
		if _, ok := have[name]; !ok {
			t.Errorf("the archive does not hold %s", name)
		}
	}
}

func fstestTree(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

// entries reads every regular file of a tree into memory.
func entries(src fs.FS) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		out[p] = data
		return nil
	})
	return out, err
}
