package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The build files live at the repository root, two directories above this
// package.
const (
	repoRoot   = "../.."
	devPath    = repoRoot + "/" + DevDockerfile
	ciPath     = repoRoot + "/" + CIDockerfile
	deployPath = repoRoot + "/" + DefaultDeployRoot
)

func TestShippedDockerfilesHoldTheImageContract(t *testing.T) {
	problems, err := CheckDockerfiles(devPath, ciPath)
	if err != nil {
		t.Fatalf("CheckDockerfiles: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("the shipped build files break the contract:\n  %s", strings.Join(problems, "\n  "))
	}
}

// mutated copies both build files into a temporary directory and applies a
// replacement to one of them, so a test states the fault it wants.
func mutated(t *testing.T, file, from, to string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dev := copyFile(t, devPath, dir)
	ci := copyFile(t, ciPath, dir)

	target := filepath.Join(dir, file)
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	replaced := strings.Replace(string(data), from, to, 1)
	if replaced == string(data) {
		t.Fatalf("the mutation %q was not applied to %s", from, file)
	}
	if err := os.WriteFile(target, []byte(replaced), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
	return dev, ci
}

// Every gate here guards a property of the released image, so each one is
// exercised against a file that breaks it.
func TestCheckDockerfilesCatchesEachBrokenProperty(t *testing.T) {
	cases := map[string]struct {
		file, from, to, want string
	}{
		"runtime stage drifted apart": {
			CIDockerfile, "EXPOSE 8080", "EXPOSE 9090",
			"shared runtime stage region differs",
		},
		"base pinned by tag": {
			DevDockerfile,
			"gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab",
			"gcr.io/distroless/static-debian12:nonroot",
			"not pinned by digest",
		},
		"base with a shell": {
			DevDockerfile,
			"gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab",
			"debian:12@sha256:" + strings.Repeat("a", 64),
			"shell-free",
		},
		"runs as root": {
			DevDockerfile, "USER 65532:65532", "USER 0:0",
			"runs as uid 0",
		},
		"user by name": {
			DevDockerfile, "USER 65532:65532", "USER nonroot",
			"not numeric",
		},
		"command in the runtime stage": {
			DevDockerfile, "WORKDIR /\nEXPOSE", "WORKDIR /\nRUN mkdir /data\nEXPOSE",
			"needs a shell",
		},
		"paths not stripped": {
			DevDockerfile, "\n        -trimpath \\", "\n        -v \\",
			"no -trimpath",
		},
		"build identifier kept": {
			DevDockerfile, "-s -w -buildid=", "-s -w",
			"no -buildid=",
		},
		"release image compiles": {
			CIDockerfile, "ARG BINARY", "ARG BINARY\nRUN go build ./...",
			"compiles the binary",
		},
		"binary argument defaulted": {
			CIDockerfile, "ARG BINARY", "ARG BINARY=out/service",
			"would silently package a stale file",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dev, ci := mutated(t, c.file, c.from, c.to)
			problems, err := CheckDockerfiles(dev, ci)
			if err != nil {
				t.Fatalf("CheckDockerfiles: %v", err)
			}
			if !problemsContain(problems, c.want) {
				t.Fatalf("no problem mentions %q; got %v", c.want, problems)
			}
		})
	}
}

func TestRegionReportsMissingMarkers(t *testing.T) {
	if _, err := Region("no markers here", RuntimeStageStart, RuntimeStageEnd); err == nil {
		t.Error("Region accepted content with no start marker")
	}
	if _, err := Region(RuntimeStageStart+"\nbody\n", RuntimeStageStart, RuntimeStageEnd); err == nil {
		t.Error("Region accepted an unterminated region")
	}
	body, err := Region(RuntimeStageStart+"\nbody\n"+RuntimeStageEnd, RuntimeStageStart, RuntimeStageEnd)
	if err != nil || body != "body" {
		t.Errorf("Region = %q, %v", body, err)
	}
}

func TestCheckDockerfilesReportsAMissingFile(t *testing.T) {
	if _, err := CheckDockerfiles(filepath.Join(t.TempDir(), "absent"), ciPath); err == nil {
		t.Error("CheckDockerfiles accepted a missing developer file")
	}
	if _, err := CheckDockerfiles(devPath, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("CheckDockerfiles accepted a missing release file")
	}
}

func TestCheckDockerfilesReportsMissingRegions(t *testing.T) {
	dir := t.TempDir()
	bare := writeFile(t, dir, "Dockerfile.bare", "FROM scratch\n")
	problems, err := CheckDockerfiles(bare, bare)
	if err != nil {
		t.Fatalf("CheckDockerfiles: %v", err)
	}
	if !problemsContain(problems, RuntimeBaseStart) || !problemsContain(problems, RuntimeStageStart) {
		t.Fatalf("problems do not name the missing regions: %v", problems)
	}
}
