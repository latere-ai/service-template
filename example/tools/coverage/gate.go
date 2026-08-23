package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// DefaultThreshold is the statement coverage a repository must reach when the
// declaration does not state one.
const DefaultThreshold = 90

// PackageMargin is how far below the threshold one package may sit. A high
// overall figure otherwise hides a single untested package, which is the
// failure the per-package rule exists to catch.
const PackageMargin = 20

// Declaration is the part of .template.yaml this gate reads. The exclusion
// list is explicit paths and never patterns, so adding one shows in the diff
// and cannot silently grow to cover a package it was not meant to.
type Declaration struct {
	Module   string `yaml:"module"`
	Coverage struct {
		Threshold *int     `yaml:"threshold"`
		Exclude   []string `yaml:"exclude"`
	} `yaml:"coverage"`
}

// ReadDeclaration parses the declaration at path. A missing file is not an
// error: the defaults the contract documents apply, and the caller reports
// which source was used.
func ReadDeclaration(path string) (Declaration, bool, error) {
	var d Declaration
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, false, nil
	}
	if err != nil {
		return d, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &d); err != nil {
		return d, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return d, true, nil
}

// Threshold is the configured gate, or the documented default.
func (d Declaration) Threshold() int {
	if d.Coverage.Threshold == nil {
		return DefaultThreshold
	}
	return *d.Coverage.Threshold
}

// IsExcluded reports whether a repository-relative package path is outside the
// denominator. An entry that ends in a slash covers a directory and everything
// under it; any other entry matches exactly one package.
func (d Declaration) IsExcluded(pkgPath string) bool {
	for _, raw := range d.Coverage.Exclude {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if dir, ok := strings.CutSuffix(entry, "/"); ok {
			if pkgPath == dir || strings.HasPrefix(pkgPath, dir+"/") {
				return true
			}
			continue
		}
		if pkgPath == entry {
			return true
		}
	}
	return false
}

// Report is the merged coverage figure the gate judges.
type Report struct {
	Threshold int
	Packages  []PackageCoverage
	// Missing lists packages that exist in the module but produced no
	// coverage data. A package with no test file does not appear in a plain
	// profile, so without this it would escape the per-package rule.
	Missing []string
}

// Totals returns the covered and total statement counts over the packages
// inside the denominator.
func (r Report) Totals() (covered, total int) {
	for _, p := range r.Packages {
		if p.Excluded {
			continue
		}
		covered += p.Covered
		total += p.Statements
	}
	return covered, total
}

// Percent is the overall statement coverage. An empty denominator reports zero
// rather than dividing, because "no statements measured" must not read as a
// pass.
func (r Report) Percent() float64 {
	covered, total := r.Totals()
	if total == 0 {
		return 0
	}
	return 100 * float64(covered) / float64(total)
}

// Problems lists every reason the gate fails, in report order. An empty result
// is a pass.
func (r Report) Problems() []string {
	var problems []string
	covered, total := r.Totals()
	if total == 0 {
		problems = append(problems,
			"no statements were measured; the coverage profiles are empty, so the gate did not run")
	} else if r.Percent() < float64(r.Threshold) {
		problems = append(problems, fmt.Sprintf(
			"overall coverage is %.1f%% (%d of %d statements), below the %d%% threshold",
			r.Percent(), covered, total, r.Threshold))
	}
	floor := float64(r.Threshold - PackageMargin)
	for _, p := range r.Packages {
		if p.Excluded || p.Statements == 0 {
			continue
		}
		if p.Percent() < floor {
			problems = append(problems, fmt.Sprintf(
				"package %s is at %.1f%%, more than %d points below the %d%% threshold",
				p.Path, p.Percent(), PackageMargin, r.Threshold))
		}
	}
	for _, m := range r.Missing {
		problems = append(problems, fmt.Sprintf(
			"package %s produced no coverage data; build the profile with -coverpkg=./... so every package is measured", m))
	}
	return problems
}

// Build folds the parsed profiles and the declared package list into a report.
// known is every package the module holds, used to catch a package the profile
// omits entirely.
func Build(set *profileSet, d Declaration, known []string) Report {
	pkgs := set.Packages(d.Module)
	present := map[string]bool{}
	for i := range pkgs {
		pkgs[i].Excluded = d.IsExcluded(pkgs[i].Path)
		present[pkgs[i].Path] = true
	}
	var missing []string
	for _, k := range known {
		if !present[k] && !d.IsExcluded(k) {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	return Report{Threshold: d.Threshold(), Packages: pkgs, Missing: missing}
}

// Write prints the per-package figures and the overall line. The gate reports
// the numbers whether it passes or fails, because a gate that prints only on
// failure gives a reviewer nothing to compare against.
func (r Report) Write(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PACKAGE\tCOVERAGE\tSTATEMENTS"); err != nil {
		return err
	}
	for _, p := range r.Packages {
		note := ""
		if p.Excluded {
			note = "\texcluded"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%.1f%%\t%d%s\n", p.Path, p.Percent(), p.Statements, note); err != nil {
			return err
		}
	}
	covered, total := r.Totals()
	if _, err := fmt.Fprintf(tw, "\noverall\t%.1f%%\t%d of %d\tthreshold %d%%\n",
		r.Percent(), covered, total, r.Threshold); err != nil {
		return err
	}
	return tw.Flush()
}
