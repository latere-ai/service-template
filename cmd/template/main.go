// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Command template materializes the files a service template owns and proves
// that a consumer repository still matches the version it claims.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"latere.ai/x/service-template/internal/generator"
	"latere.ai/x/service-template/internal/skeleton"
)

// version is the generator's own release. A release build may set it with
// -ldflags "-X main.version=v1.4.0"; otherwise it comes from the build
// information the Go tool records.
var version = ""

// skeletonEnv names a directory that holds a skeleton tree to generate from
// instead of the one this build carries.
const skeletonEnv = "TEMPLATE_SKELETON"

func main() {
	src, embedded, rest, err := skeletonSource(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "template: %v\n", err)
		os.Exit(generator.ExitError)
	}
	info, ok := debug.ReadBuildInfo()
	v, why := buildVersion(version, info, ok)
	env := generator.Env{
		Skeleton:       src,
		Embedded:       embedded,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		Version:        v,
		VersionMissing: why,
	}
	os.Exit(generator.Run(env, rest))
}

// buildVersion is the template release this build is, and when it is none,
// the reason, for the error that asks for one.
//
// The Go tool records the module version of a build: the release for
// `go run` or `go install` of latere.ai/x/service-template/cmd/template@<version>,
// and for `go build` in a checkout the version control stamp, which is the
// tag of a tagged commit or a pseudo-version naming the commit. A pseudo-version
// resolves through the module proxy like a release, so a service scaffolded
// from a pushed commit can be checked against that commit later.
//
// A plain `go run` in a checkout stamps nothing, and a checkout with
// uncommitted changes stamps a version that no module download can
// reproduce. Neither is recorded, because a declaration names the files a
// service came from, and neither build can name them.
func buildVersion(linked string, info *debug.BuildInfo, ok bool) (string, string) {
	if linked != "" {
		return linked, ""
	}
	if !ok || info == nil {
		return "", "the binary holds no build information"
	}
	v := info.Main.Version
	switch {
	case v == "" || v == "(devel)":
		return "", "it was built without version control stamping, which a plain go run in a checkout does"
	case strings.Contains(v, "+dirty"):
		return "", "it was built from a checkout with uncommitted changes (" + v + "), which no release holds"
	}
	return v, ""
}

// skeletonSource resolves the skeleton tree and returns the arguments with the
// -skeleton flag removed. The flag wins over the environment variable, and
// both win over the tree this build carries. embedded reports that the tree
// is the carried one, whose content the build's version names exactly.
//
// A working directory never selects the tree. A command run inside a checkout
// of the template, or inside a service scaffolded next to one, generates from
// the tree of its own release unless it is told otherwise.
func skeletonSource(args []string, getenv func(string) string) (fs.FS, bool, []string, error) {
	rest := make([]string, 0, len(args))
	from := ""
	source := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-skeleton" || args[i] == "--skeleton":
			if i+1 >= len(args) {
				return nil, false, nil, fmt.Errorf("-skeleton needs a directory")
			}
			from, source = args[i+1], "-skeleton"
			i++
		case strings.HasPrefix(args[i], "-skeleton="):
			from, source = strings.TrimPrefix(args[i], "-skeleton="), "-skeleton"
		case strings.HasPrefix(args[i], "--skeleton="):
			from, source = strings.TrimPrefix(args[i], "--skeleton="), "-skeleton"
		default:
			rest = append(rest, args[i])
		}
	}
	if from == "" {
		from, source = getenv(skeletonEnv), skeletonEnv
	}
	if from != "" {
		if !isSkeleton(from) {
			return nil, false, nil, fmt.Errorf("%s names %s, which holds no %s directory",
				source, from, generator.ManifestDir)
		}
		return os.DirFS(from), false, rest, nil
	}
	carried, err := skeleton.FS()
	if err != nil {
		return nil, false, nil, err
	}
	return carried, true, rest, nil
}

func isSkeleton(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, generator.ManifestDir))
	return err == nil && st.IsDir()
}
