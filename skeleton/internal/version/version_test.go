package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(settings ...debug.BuildSetting) buildInfoReader {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: settings}, true
	}
}

func noBuildInfo() (*debug.BuildInfo, bool) { return nil, false }

func TestResolveUsesLinkerValues(t *testing.T) {
	got := resolve("v1.2.3", "abcdef", "2026-01-02T03:04:05Z", "sha256-assets", noBuildInfo)
	want := Build{
		Version:   "v1.2.3",
		Commit:    "abcdef",
		BuildTime: "2026-01-02T03:04:05Z",
		AssetHash: "sha256-assets",
	}
	if got != want {
		t.Fatalf("resolve = %+v, want %+v", got, want)
	}
}

func TestResolveDefaultsWithoutAnySource(t *testing.T) {
	got := resolve("", "", "", "", noBuildInfo)
	want := Build{Version: "dev", Commit: unknown, BuildTime: unknown, AssetHash: unknown}
	if got != want {
		t.Fatalf("resolve = %+v, want %+v", got, want)
	}
}

func TestResolveFallsBackToVersionControlInfo(t *testing.T) {
	read := buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"},
		debug.BuildSetting{Key: "vcs.time", Value: "2026-02-03T04:05:06Z"},
		debug.BuildSetting{Key: "vcs.modified", Value: "false"},
	)
	got := resolve("", "", "", "", read)
	if got.Version != "dev" {
		t.Errorf("Version = %q, want dev", got.Version)
	}
	if got.Commit != "0123456789abcdef" {
		t.Errorf("Commit = %q, want the embedded revision", got.Commit)
	}
	if got.BuildTime != "2026-02-03T04:05:06Z" {
		t.Errorf("BuildTime = %q, want the embedded commit time", got.BuildTime)
	}
}

func TestResolveMarksDirtyTree(t *testing.T) {
	read := buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	)
	got := resolve("", "", "", "", read)
	if !strings.HasSuffix(got.Commit, dirtySuffix) {
		t.Fatalf("Commit = %q, want a %q suffix", got.Commit, dirtySuffix)
	}
}

func TestResolveDoesNotDoubleTheDirtySuffix(t *testing.T) {
	read := buildInfo(debug.BuildSetting{Key: "vcs.modified", Value: "true"})
	got := resolve("", "0123456789abcdef"+dirtySuffix, "", "", read)
	if got.Commit != "0123456789abcdef"+dirtySuffix {
		t.Fatalf("Commit = %q, want exactly one %q suffix", got.Commit, dirtySuffix)
	}
}

func TestResolveLeavesUnknownCommitUnmarked(t *testing.T) {
	read := buildInfo(debug.BuildSetting{Key: "vcs.modified", Value: "true"})
	got := resolve("", "", "", "", read)
	if got.Commit != unknown {
		t.Fatalf("Commit = %q, want %q with no revision available", got.Commit, unknown)
	}
}

func TestInfoIsAlwaysPopulated(t *testing.T) {
	got := Info()
	if got.Version == "" || got.Commit == "" || got.BuildTime == "" || got.AssetHash == "" {
		t.Fatalf("Info = %+v, want every field populated", got)
	}
}

func TestBuildString(t *testing.T) {
	b := Build{Version: "v1", Commit: "c", BuildTime: "t", AssetHash: "a"}
	want := "version=v1 commit=c built=t assets=a"
	if got := b.String(); got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

func TestFirstNonEmptyWithNoValues(t *testing.T) {
	if got := firstNonEmpty(); got != "" {
		t.Errorf("firstNonEmpty() = %q, want an empty string", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty of empty values = %q, want an empty string", got)
	}
}
