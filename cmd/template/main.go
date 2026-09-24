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

// version is the generator's own release. A release build sets it with
// -ldflags "-X main.version=v1.4.0"; a build from source falls back to the
// module version the Go tool recorded.
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
	env := generator.Env{
		Skeleton: src,
		Embedded: embedded,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Version:  buildVersion(),
	}
	os.Exit(generator.Run(env, rest))
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return ""
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
