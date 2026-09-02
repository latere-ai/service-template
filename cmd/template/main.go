// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Command template materializes the files a service template owns and proves
// that a consumer repository still matches the version it claims.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"latere.ai/x/service-template/internal/generator"
)

// version is the generator's own release. A release build sets it with
// -ldflags "-X main.version=v1.4.0"; a build from source falls back to the
// module version the Go tool recorded.
var version = ""

// skeletonEnv names the directory that holds the skeleton tree. The skeleton
// cannot be embedded, because an embed pattern may not leave the package
// directory, so the command locates it on disk.
const skeletonEnv = "TEMPLATE_SKELETON"

func main() {
	args := os.Args[1:]
	dir, rest, err := skeletonDir(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "template: %v\n", err)
		os.Exit(generator.ExitError)
	}
	env := generator.Env{
		Skeleton: os.DirFS(dir),
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

// skeletonDir resolves the skeleton tree and returns the arguments with the
// -skeleton flag removed. The flag wins over the environment variable, and a
// search from the working directory and the executable covers a build from a
// checkout.
func skeletonDir(args []string) (string, []string, error) {
	rest := make([]string, 0, len(args))
	from := ""
	source := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-skeleton" || args[i] == "--skeleton":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("-skeleton needs a directory")
			}
			from, source = args[i+1], "-skeleton"
			i++
		case len(args[i]) > 10 && (args[i][:10] == "-skeleton="):
			from, source = strings.TrimPrefix(args[i], "-skeleton="), "-skeleton"
		case strings.HasPrefix(args[i], "--skeleton="):
			from, source = strings.TrimPrefix(args[i], "--skeleton="), "-skeleton"
		default:
			rest = append(rest, args[i])
		}
	}
	if from == "" {
		from, source = os.Getenv(skeletonEnv), skeletonEnv
	}
	if from != "" {
		if !isSkeleton(from) {
			return "", nil, fmt.Errorf("%s names %s, which holds no %s directory",
				source, from, generator.ManifestDir)
		}
		return from, rest, nil
	}
	if found, ok := searchUp(); ok {
		return found, rest, nil
	}
	return "", nil, fmt.Errorf(
		"cannot find the skeleton tree; pass -skeleton <dir> or set %s", skeletonEnv)
}

func isSkeleton(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, generator.ManifestDir))
	return err == nil && st.IsDir()
}

// searchUp walks upwards from the working directory and from the executable
// looking for a checkout of the template.
func searchUp() (string, bool) {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, start := range starts {
		dir := start
		for {
			candidate := filepath.Join(dir, "skeleton")
			if isSkeleton(candidate) {
				return candidate, true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", false
}
