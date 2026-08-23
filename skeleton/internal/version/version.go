// Package version exposes the build metadata compiled into the binary.
//
// The values are set at link time with -ldflags -X by the build target. When
// they are absent, which is the case for `go run` and for `go test`, the
// package falls back to the version control information the toolchain embeds
// in the binary, so a locally built binary still identifies its commit.
package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// Linker-set values. The build target overrides each one with
// -X example.com/service/internal/version.<name>=<value>.
var (
	version   string
	commit    string
	buildTime string
	assetHash string
)

// unknown is the value reported for a field that neither the linker nor the
// embedded version control information supplied.
const unknown = "unknown"

// dirtySuffix marks a binary built from a working tree with uncommitted
// changes. Such a binary is not reproducible from its commit alone.
const dirtySuffix = "-dirty"

// Build is the metadata a binary reports about itself.
type Build struct {
	// Version is the release tag, or "dev" for a build outside a release.
	Version string
	// Commit is the full commit SHA, with a "-dirty" suffix when the working
	// tree held uncommitted changes at build time.
	Commit string
	// BuildTime is the build timestamp in RFC 3339, UTC.
	BuildTime string
	// AssetHash identifies the embedded frontend bundle, or "unknown" when the
	// binary embeds none.
	AssetHash string
}

// String renders the build in one line for a version flag or a log record.
func (b Build) String() string {
	return fmt.Sprintf("version=%s commit=%s built=%s assets=%s",
		b.Version, b.Commit, b.BuildTime, b.AssetHash)
}

// Info reports the build metadata of the running binary.
func Info() Build {
	return resolve(version, commit, buildTime, assetHash, readBuildInfo)
}

// buildInfoReader reads the version control information the toolchain embeds.
// It is a variable so tests can supply a known state.
type buildInfoReader func() (*debug.BuildInfo, bool)

func readBuildInfo() (*debug.BuildInfo, bool) { return debug.ReadBuildInfo() }

// resolve merges the linker-set values with the embedded version control
// information. Linker values win, because the build target computes them from
// the same tree and knows the release tag, which the embedded data does not
// carry.
func resolve(version, commit, buildTime, assetHash string, read buildInfoReader) Build {
	vcsRevision, vcsTime, vcsModified := vcsInfo(read)

	b := Build{
		Version:   firstNonEmpty(version, "dev"),
		Commit:    firstNonEmpty(commit, vcsRevision, unknown),
		BuildTime: firstNonEmpty(buildTime, vcsTime, unknown),
		AssetHash: firstNonEmpty(assetHash, unknown),
	}
	// The linker-set commit already carries the suffix when the build target
	// saw a dirty tree. Adding it again would produce "sha-dirty-dirty".
	if vcsModified && b.Commit != unknown && !strings.HasSuffix(b.Commit, dirtySuffix) {
		b.Commit += dirtySuffix
	}
	return b
}

// vcsInfo extracts the revision, the commit time, and the dirty flag from the
// embedded build information.
func vcsInfo(read buildInfoReader) (revision, buildTime string, modified bool) {
	info, ok := read()
	if !ok || info == nil {
		return "", "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			buildTime = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return revision, buildTime, modified
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
