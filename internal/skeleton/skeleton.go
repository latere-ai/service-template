// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package skeleton carries the skeleton tree inside the template command, so a
// build of the command generates and checks from the tree of its own release
// with nothing else on disk.
//
// The tree cannot be embedded where it lives. skeleton/ is a Go module of its
// own, so that the shipped code compiles and its tests run before any service
// receives it, and a nested module is neither reachable by an embed pattern
// nor part of this module's download: a command installed with
// `go install latere.ai/x/service-template/cmd/template@<version>` would get a
// module with no skeleton in it. The tree is therefore packed into one
// uncompressed zip archive, committed beside this file, and a test holds the
// archive to the tree it was packed from.
package skeleton

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"sync"
	"time"

	"latere.ai/x/service-template/internal/generator"
)

// ArchiveName is the committed archive, relative to this package.
const ArchiveName = "skeleton.zip"

//go:embed skeleton.zip
var archive []byte

var open = sync.OnceValues(func() (fs.FS, error) {
	r, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("read the embedded skeleton archive: %w", err)
	}
	return r, nil
})

// FS returns the skeleton tree this build carries, rooted at the directory
// that holds manifests/.
func FS() (fs.FS, error) { return open() }

// packTime is the modification time every archive entry records. A fixed value
// keeps the archive a function of the tree alone.
var packTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Files lists what the archive holds for a skeleton tree: every manifest
// fragment and every file a fragment declares, in path order. Nothing else is
// read at generation, and a working tree also holds build output, installed
// dependencies, and rendered lint configuration that no service receives.
func Files(src fs.FS) ([]string, error) {
	m, err := generator.LoadManifest(src)
	if err != nil {
		return nil, err
	}
	fragments, err := fs.Glob(src, path.Join(generator.ManifestDir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifest fragments: %w", err)
	}
	files := append([]string(nil), fragments...)
	for _, e := range m.Entries {
		if e.Source == "" {
			return nil, fmt.Errorf("%s declares %q but the skeleton holds no such file", e.Fragment, e.Path)
		}
		files = append(files, e.Source)
	}
	sort.Strings(files)
	return files, nil
}

// Pack builds the archive for a skeleton tree. Entries are stored rather than
// compressed: the module download compresses the file anyway, and git stores
// the difference between two uncompressed archives far more compactly than
// between two compressed ones.
func Pack(src fs.FS) ([]byte, error) {
	files, err := Files(src)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range files {
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return nil, fmt.Errorf("read skeleton file %s: %w", name, err)
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: packTime})
		if err != nil {
			return nil, fmt.Errorf("add %s to the archive: %w", name, err)
		}
		if _, err := f.Write(data); err != nil {
			return nil, fmt.Errorf("add %s to the archive: %w", name, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("finish the archive: %w", err)
	}
	return buf.Bytes(), nil
}
