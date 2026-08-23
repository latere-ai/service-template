package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The two build files share these regions verbatim. The developer image and
// the released image must differ only in how the binary arrived, and a shared
// region that has drifted is how the two silently become different artifacts.
const (
	RuntimeBaseStart  = "# >>> shared runtime base <<<"
	RuntimeBaseEnd    = "# >>> end shared runtime base <<<"
	RuntimeStageStart = "# >>> shared runtime stage <<<"
	RuntimeStageEnd   = "# >>> end shared runtime stage <<<"
)

// Paths of the two build files.
const (
	DevDockerfile = "Dockerfile"
	CIDockerfile  = "Dockerfile.ci"
)

// baseDigestPattern matches a base image pinned by digest.
var baseDigestPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}`)

// runtimeBasePattern captures the runtime base the shared region declares.
var runtimeBasePattern = regexp.MustCompile(`(?m)^ARG\s+RUNTIME_BASE=(\S+)`)

// userPattern captures the user the runtime stage runs as.
var userPattern = regexp.MustCompile(`(?m)^USER\s+(\S+)`)

// instructionPattern captures the leading instruction of a Dockerfile line.
var instructionPattern = regexp.MustCompile(`(?m)^([A-Z]+)\s`)

// Region returns the text between two markers, excluding the marker lines. A
// missing or unbalanced marker is an error, because a region that is not
// delimited cannot be compared.
func Region(content, start, end string) (string, error) {
	_, after, found := strings.Cut(content, start)
	if !found {
		return "", fmt.Errorf("no %q marker", start)
	}
	body, _, found := strings.Cut(after, end)
	if !found {
		return "", fmt.Errorf("no %q marker after %q", end, start)
	}
	return strings.Trim(body, "\n"), nil
}

// CheckDockerfiles reports every way the two build files break the image
// contract. It returns the problems rather than the first one, so one run
// fixes the files.
func CheckDockerfiles(devPath, ciPath string) ([]string, error) {
	dev, err := os.ReadFile(devPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", devPath, err)
	}
	ci, err := os.ReadFile(ciPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ciPath, err)
	}
	devText, ciText := string(dev), string(ci)

	var problems []string
	problems = append(problems, checkSharedRegions(devPath, devText, ciPath, ciText)...)
	problems = append(problems, checkRuntimeBase(devPath, devText)...)
	problems = append(problems, checkRuntimeStage(devPath, devText)...)
	problems = append(problems, checkReproducibleBuild(devPath, devText)...)
	problems = append(problems, checkPackagedBinary(ciPath, ciText)...)
	return problems, nil
}

// checkSharedRegions asserts both files carry both regions with the same text.
func checkSharedRegions(devPath, dev, ciPath, ci string) []string {
	var problems []string
	for _, region := range []struct{ start, end, label string }{
		{RuntimeBaseStart, RuntimeBaseEnd, "runtime base"},
		{RuntimeStageStart, RuntimeStageEnd, "runtime stage"},
	} {
		devRegion, devErr := Region(dev, region.start, region.end)
		ciRegion, ciErr := Region(ci, region.start, region.end)
		if devErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", devPath, devErr))
		}
		if ciErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", ciPath, ciErr))
		}
		if devErr != nil || ciErr != nil {
			continue
		}
		if devRegion != ciRegion {
			problems = append(problems, fmt.Sprintf(
				"the shared %s region differs between %s and %s; the two images would not share a runtime",
				region.label, devPath, ciPath))
		}
	}
	return problems
}

// checkRuntimeBase asserts the base is minimal and pinned by digest. A tag can
// move; a digest cannot, and a moving base makes the provenance claim
// unprovable.
func checkRuntimeBase(path, content string) []string {
	var problems []string
	region, err := Region(content, RuntimeBaseStart, RuntimeBaseEnd)
	if err != nil {
		return nil
	}
	m := runtimeBasePattern.FindStringSubmatch(region)
	if m == nil {
		problems = append(problems, fmt.Sprintf("%s: the shared region declares no RUNTIME_BASE", path))
		return problems
	}
	base := m[1]
	if !baseDigestPattern.MatchString(base) {
		problems = append(problems, fmt.Sprintf("%s: base %q is not pinned by digest", path, base))
	}
	// The static distribution carries a certificate bundle, a time zone
	// database, and nothing that can execute a command.
	if !strings.Contains(base, "distroless/static") {
		problems = append(problems, fmt.Sprintf(
			"%s: base %q is not a shell-free distribution; the runtime image must hold no shell and no package manager",
			path, base))
	}
	return problems
}

// checkRuntimeStage asserts the runtime runs as a non-root user and installs
// nothing. An instruction that runs a command in the runtime stage needs a
// shell, which the image does not have.
func checkRuntimeStage(path, content string) []string {
	region, err := Region(content, RuntimeStageStart, RuntimeStageEnd)
	if err != nil {
		return nil
	}
	var problems []string
	m := userPattern.FindStringSubmatch(region)
	if m == nil {
		problems = append(problems, fmt.Sprintf("%s: the runtime stage sets no USER, so the container runs as root", path))
	} else {
		uid, _, _ := strings.Cut(m[1], ":")
		n, err := strconv.Atoi(uid)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf(
				"%s: USER %q is not numeric; a runAsNonRoot check cannot resolve a user name", path, m[1]))
		case n == 0:
			problems = append(problems, fmt.Sprintf("%s: the runtime stage runs as uid 0", path))
		}
	}
	for _, instr := range instructionPattern.FindAllStringSubmatch(region, -1) {
		switch instr[1] {
		case "RUN", "ADD", "SHELL":
			problems = append(problems, fmt.Sprintf(
				"%s: the runtime stage uses %s, which needs a shell or fetches at build time", path, instr[1]))
		}
	}
	return problems
}

// checkReproducibleBuild asserts the compile step removes the inputs that vary
// between machines and runs. Two builds of one commit have to produce the same
// bytes, which is what makes the provenance claim checkable rather than
// decorative.
func checkReproducibleBuild(path, content string) []string {
	// Comments are removed first, because a checker that a comment can
	// satisfy proves nothing about the build.
	content = stripComments(content)
	var problems []string
	for _, required := range []struct{ needle, why string }{
		{"-trimpath", "the build machine's paths would be recorded in the binary"},
		{"-buildid=", "the build identifier varies with the linker's temporary paths"},
		{"CGO_ENABLED=0", "a dynamically linked binary depends on the builder's libraries"},
		{"SOURCE_DATE_EPOCH", "the build time would be the moment the build ran"},
		{"GOTOOLCHAIN=local", "the build would fetch whatever toolchain go.mod names"},
	} {
		if !strings.Contains(content, required.needle) {
			problems = append(problems, fmt.Sprintf("%s: no %s, so %s", path, required.needle, required.why))
		}
	}
	return problems
}

// stripComments removes comment lines, leaving the instructions.
func stripComments(content string) string {
	var kept []string
	for line := range strings.SplitSeq(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// checkPackagedBinary asserts the release file compiles nothing. The pipeline
// packages the binary the verify run already built and tested, so the tested
// artifact and the shipped artifact are the same bytes.
func checkPackagedBinary(path, content string) []string {
	content = stripComments(content)
	var problems []string
	if strings.Contains(content, "go build") {
		problems = append(problems, fmt.Sprintf(
			"%s: compiles the binary; the release image must package the artifact the verify run produced", path))
	}
	if !strings.Contains(content, "ARG BINARY") {
		problems = append(problems, fmt.Sprintf("%s: declares no BINARY argument naming the prebuilt artifact", path))
	}
	if regexp.MustCompile(`(?m)^ARG\s+BINARY=`).MatchString(content) {
		problems = append(problems, fmt.Sprintf(
			"%s: BINARY has a default, which would silently package a stale file", path))
	}
	if n := strings.Count(content, "\nFROM "); n > 1 {
		problems = append(problems, fmt.Sprintf("%s: has %d stages; the release image needs only the runtime stage", path, n))
	}
	return problems
}
