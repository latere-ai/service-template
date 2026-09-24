// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
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

// The version a build records is the one the Go tool stamped, and only one
// that names files a module download can reproduce.
func TestBuildVersion(t *testing.T) {
	stamped := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: generator.CommandPath, Version: v}}
	}
	cases := []struct {
		name   string
		linked string
		info   *debug.BuildInfo
		ok     bool
		want   string
		why    string
	}{
		{"a release run through the module proxy", "", stamped("v1.0.0"), true, "v1.0.0", ""},
		{"a clean checkout past a release", "", stamped("v1.0.1-0.20260924120120-e26584391406"), true,
			"v1.0.1-0.20260924120120-e26584391406", ""},
		{"a clean checkout before any release", "", stamped("v0.0.0-20260924120120-e26584391406"), true,
			"v0.0.0-20260924120120-e26584391406", ""},
		{"a version set at link time", "v1.2.3", stamped("(devel)"), true, "v1.2.3", ""},
		{"a plain go run in a checkout", "", stamped("(devel)"), true, "", "without version control stamping"},
		{"no module version at all", "", stamped(""), true, "", "without version control stamping"},
		{"a checkout with uncommitted changes", "", stamped("v1.0.1-0.20260924120120-e26584391406+dirty"), true,
			"", "uncommitted changes"},
		{"no build information", "", nil, false, "", "no build information"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := buildVersion(c.linked, c.info, c.ok)
			if got != c.want {
				t.Errorf("version %q, want %q", got, c.want)
			}
			if c.why == "" && why != "" {
				t.Errorf("a versioned build gave a reason: %q", why)
			}
			if !strings.Contains(why, c.why) {
				t.Errorf("reason %q does not say %q", why, c.why)
			}
		})
	}
}
