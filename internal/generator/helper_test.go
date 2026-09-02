// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testVersion = "v1.4.0"

var testNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// skeletonFS is the fixture skeleton read straight from testdata.
func skeletonFS(t *testing.T) fs.FS {
	t.Helper()
	return os.DirFS("testdata/skeleton")
}

// mutableSkeleton copies the fixture skeleton into a temporary directory so a
// test can move the template forward and observe the "behind" verdict.
func mutableSkeleton(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "testdata/skeleton"
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the fixture skeleton: %v", err)
	}
	return dst
}

// testConfig is a service declaration with the frontend and database flags on.
func testConfig() *Config {
	return &Config{
		Template: DefaultTemplate,
		Version:  testVersion,
		Module:   "github.com/acme/widget",
		Name:     "widget",
		Profile:  ProfileService,
		Features: map[string]bool{FeatureFrontend: true, FeatureDatabase: true},
	}
}

// initRepo scaffolds a repository and returns its directory.
func initRepo(t *testing.T, src fs.FS, cfg *Config) string {
	t.Helper()
	if err := cfg.Validate(ConfigFile); err != nil {
		t.Fatalf("validate the declaration: %v", err)
	}
	dir := t.TempDir()
	if _, err := Init(src, dir, cfg); err != nil {
		t.Fatalf("init: %v", err)
	}
	return dir
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func exists(t *testing.T, dir, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

// tree returns every file in a directory as path to content, so two
// generations can be compared byte for byte.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// runCLI drives the command through its exported entry point and returns the
// exit code with both streams.
func runCLI(t *testing.T, src fs.FS, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(Env{Skeleton: src, Stdout: &out, Stderr: &errOut, Now: testNow, Version: testVersion}, args)
	return code, out.String(), errOut.String()
}

func mustContain(t *testing.T, got, want, what string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s: expected %q in:\n%s", what, want, got)
	}
}
