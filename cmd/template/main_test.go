// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/service-template/internal/generator"
)

func noEnv(string) string { return "" }

// An installed command runs in a service repository, far from any checkout of
// the template, and has to find the skeleton of its own release there.
func TestTheCommandCarriesItsSkeleton(t *testing.T) {
	t.Chdir(t.TempDir())
	src, embedded, rest, err := skeletonSource([]string{"check", "-C", "."}, noEnv)
	if err != nil {
		t.Fatalf("resolve the skeleton outside a checkout: %v", err)
	}
	if !embedded {
		t.Fatal("the carried skeleton is not reported as embedded")
	}
	if strings.Join(rest, " ") != "check -C ." {
		t.Fatalf("the remaining arguments are %q", rest)
	}
	if _, err := generator.LoadManifest(src); err != nil {
		t.Fatalf("the carried skeleton holds no manifest: %v", err)
	}
}

// A working directory never selects the tree: a command run inside a checkout
// of the template uses its own release unless it is told otherwise.
func TestAWorkingDirectoryDoesNotSelectTheSkeleton(t *testing.T) {
	root := t.TempDir()
	writeSkeleton(t, filepath.Join(root, "skeleton"))
	inside := filepath.Join(root, "service")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inside)
	_, embedded, _, err := skeletonSource(nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !embedded {
		t.Fatal("a skeleton tree above the working directory replaced the carried one")
	}
}

// -skeleton and TEMPLATE_SKELETON name another tree, for a change to the
// template itself; the flag wins over the variable.
func TestAnExplicitTreeReplacesTheCarriedOne(t *testing.T) {
	flagged := filepath.Join(t.TempDir(), "flagged")
	writeSkeleton(t, flagged)
	env := filepath.Join(t.TempDir(), "env")
	writeSkeleton(t, env)
	getenv := func(k string) string {
		if k == skeletonEnv {
			return env
		}
		return ""
	}

	for _, args := range [][]string{
		{"-skeleton", flagged, "manifest"},
		{"-skeleton=" + flagged, "manifest"},
		{"--skeleton=" + flagged, "manifest"},
	} {
		src, embedded, rest, err := skeletonSource(args, getenv)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if embedded || strings.Join(rest, " ") != "manifest" || marker(t, src) != "flagged" {
			t.Fatalf("%v selected embedded=%t rest=%q marker=%q", args, embedded, rest, marker(t, src))
		}
	}

	src, embedded, _, err := skeletonSource([]string{"manifest"}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if embedded || marker(t, src) != "env" {
		t.Fatalf("%s did not select its tree", skeletonEnv)
	}

	if _, _, _, err := skeletonSource([]string{"-skeleton", t.TempDir()}, noEnv); err == nil ||
		!strings.Contains(err.Error(), "holds no manifests directory") {
		t.Fatalf("a directory with no manifest was accepted: %v", err)
	}
	if _, _, _, err := skeletonSource([]string{"-skeleton"}, noEnv); err == nil {
		t.Fatal("-skeleton with no directory was accepted")
	}
}

func writeSkeleton(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, generator.ManifestDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte(filepath.Base(dir)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func marker(t *testing.T, src fs.FS) string {
	t.Helper()
	data, err := fs.ReadFile(src, "marker")
	if err != nil {
		return ""
	}
	return string(data)
}
