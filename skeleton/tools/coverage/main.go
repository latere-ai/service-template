// Command coverage is the statement coverage gate.
//
// It merges the coverage profiles of every test tier, compares the result
// against the threshold in .template.yaml, and fails when the overall figure is
// below it or when one non-excluded package sits more than PackageMargin points
// below it. A number that is printed and never compared is a preference, not a
// standard, which is why this runs as a gate and not as a report.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// profileList collects a repeatable -profile flag.
type profileList []string

func (p *profileList) String() string { return strings.Join(*p, ",") }

func (p *profileList) Set(v string) error {
	if v == "" {
		return fmt.Errorf("a profile path cannot be empty")
	}
	*p = append(*p, v)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "coverage:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var profiles profileList
	fs := newFlagSet(&profiles)
	config := fs.String("config", ".template.yaml", "path to the template declaration")
	verify := fs.Bool("verify-packages", true,
		"fail when a package in the module produced no coverage data")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no -profile given; the gate needs at least one coverage profile")
	}

	decl, found, err := ReadDeclaration(*config)
	if err != nil {
		return err
	}
	if !found {
		report(stderr, "coverage: %s not found, using the default %d%% threshold and no exclusions\n",
			*config, DefaultThreshold)
	}
	if decl.Module == "" {
		if decl.Module, err = currentModule(); err != nil {
			return err
		}
	}

	set := newProfileSet()
	for _, path := range profiles {
		if err := addProfile(set, path); err != nil {
			return err
		}
	}

	var known []string
	if *verify {
		if known, err = modulePackages(decl.Module); err != nil {
			return err
		}
	}

	result := Build(set, decl, known)
	if err := result.Write(stdout); err != nil {
		return fmt.Errorf("write the coverage report: %w", err)
	}
	problems := result.Problems()
	if len(problems) == 0 {
		return nil
	}
	for _, p := range problems {
		report(stderr, "coverage: %s\n", p)
	}
	return fmt.Errorf("the coverage gate failed with %d problem(s)", len(problems))
}

// report writes one gate message. A write failure on the message stream is
// not worth failing the gate over, because the exit code already carries the
// verdict, so the error is dropped here and nowhere else.
func report(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// addProfile merges one coverage profile into the set.
func addProfile(set *profileSet, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open coverage profile: %w", err)
	}
	defer func() {
		// The file is read-only, so a close failure cannot lose data and the
		// parse result already stands.
		_ = f.Close()
	}()
	return set.Add(path, f)
}

// noModule is what go list -m reports outside a module. Accepting it would
// leave every package path unrelativized, so the exclusion list would silently
// match nothing.
const noModule = "command-line-arguments"

// currentModule reads the module path from the module the gate runs in.
func currentModule() (string, error) {
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("read the module path with go list -m: %w", err)
	}
	module := strings.TrimSpace(string(out))
	if module == "" || module == noModule {
		return "", fmt.Errorf("the gate is not running inside a Go module; go list -m reported %q", module)
	}
	return module, nil
}

// modulePackages lists every package in the module that holds Go source,
// relative to the repository root. A package with no test file is absent from
// a coverage profile, so the list is what makes that absence visible.
func modulePackages(module string) ([]string, error) {
	out, err := exec.Command("go", "list", "-json=ImportPath,GoFiles", "./...").Output()
	if err != nil {
		return nil, fmt.Errorf("list the module packages: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	var paths []string
	for dec.More() {
		var pkg struct {
			ImportPath string
			GoFiles    []string
		}
		if err := dec.Decode(&pkg); err != nil {
			return nil, fmt.Errorf("parse the package list: %w", err)
		}
		if len(pkg.GoFiles) == 0 {
			continue
		}
		paths = append(paths, relativeTo(module, pkg.ImportPath))
	}
	sort.Strings(paths)
	return paths, nil
}
