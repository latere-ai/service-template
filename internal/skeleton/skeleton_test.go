// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package skeleton

import (
	"archive/zip"
	"bytes"
	"flag"
	"io/fs"
	"os"
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
