package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// profileText renders a coverage profile from per-package block descriptions.
// Each entry is a package import path mapped to the statement counts of its
// blocks and whether each was reached.
func profileText(module string, pkgs map[string][][2]int) string {
	var b strings.Builder
	b.WriteString("mode: atomic\n")
	line := 10
	for pkg, blocks := range pkgs {
		for _, blk := range blocks {
			line++
			fmt.Fprintf(&b, "%s/%s/file.go:%d.1,%d.2 %d %d\n",
				module, pkg, line, line+1, blk[0], blk[1])
		}
	}
	return b.String()
}

func load(t *testing.T, texts ...string) *profileSet {
	t.Helper()
	set := newProfileSet()
	for i, text := range texts {
		if err := set.Add(fmt.Sprintf("profile-%d", i), strings.NewReader(text)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	return set
}

func decl(threshold int, exclude ...string) Declaration {
	var d Declaration
	d.Module = "github.com/example/reference-service"
	d.Coverage.Threshold = &threshold
	d.Coverage.Exclude = exclude
	return d
}

func TestParseMergesTiers(t *testing.T) {
	// One block reached only by the unit tier, one only by the integration
	// tier. Combined they are both covered, which is the point of merging.
	unit := "mode: atomic\n" +
		"github.com/example/reference-service/internal/a/f.go:1.1,2.2 3 1\n" +
		"github.com/example/reference-service/internal/a/f.go:3.1,4.2 2 0\n"
	integration := "mode: atomic\n" +
		"github.com/example/reference-service/internal/a/f.go:1.1,2.2 3 0\n" +
		"github.com/example/reference-service/internal/a/f.go:3.1,4.2 2 5\n"

	pkgs := load(t, unit, integration).Packages("github.com/example/reference-service")
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want 1: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Path != "internal/a" {
		t.Errorf("Path = %q, want internal/a", pkgs[0].Path)
	}
	if pkgs[0].Statements != 5 || pkgs[0].Covered != 5 {
		t.Errorf("covered %d of %d, want 5 of 5", pkgs[0].Covered, pkgs[0].Statements)
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	for name, text := range map[string]string{
		"too few fields":  "mode: set\ngithub.com/example/reference-service/a/f.go:1.1,2.2 3\n",
		"no span":         "mode: set\ngithub.com/example/reference-service/a/f.go 3 1\n",
		"bad statements":  "mode: set\ngithub.com/example/reference-service/a/f.go:1.1,2.2 x 1\n",
		"bad occurrences": "mode: set\ngithub.com/example/reference-service/a/f.go:1.1,2.2 3 y\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := newProfileSet().Add("p", strings.NewReader(text)); err == nil {
				t.Fatal("Add accepted a malformed profile")
			}
		})
	}
}

func TestThresholdBothDirections(t *testing.T) {
	// 9 of 10 statements is 90 percent exactly.
	text := profileText("github.com/example/reference-service", map[string][][2]int{
		"internal/a": {{9, 1}, {1, 0}},
	})
	set := load(t, text)

	at := Build(set, decl(90), nil)
	if problems := at.Problems(); len(problems) != 0 {
		t.Errorf("90%% coverage failed a 90%% threshold: %v", problems)
	}

	below := Build(set, decl(91), nil)
	problems := below.Problems()
	if len(problems) == 0 {
		t.Fatal("90% coverage passed a 91% threshold")
	}
	if !strings.Contains(problems[0], "below the 91% threshold") {
		t.Errorf("problem %q does not name the threshold", problems[0])
	}
}

func TestPackageFarBelowThresholdFailsAlone(t *testing.T) {
	// Overall is 91 percent and passes, but one package sits at 20 percent,
	// which is more than twenty points below the threshold.
	text := profileText("github.com/example/reference-service", map[string][][2]int{
		"internal/big":   {{180, 1}, {2, 0}},
		"internal/small": {{2, 1}, {8, 0}},
	})
	r := Build(load(t, text), decl(90), nil)

	if r.Percent() < 90 {
		t.Fatalf("overall is %.1f%%, the fixture must pass the overall gate", r.Percent())
	}
	problems := r.Problems()
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want exactly the per-package one: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "internal/small") {
		t.Errorf("problem %q does not name the failing package", problems[0])
	}

	// The same package at the floor passes, so the rule is a boundary and not
	// a blanket per-package threshold.
	atFloor := profileText("github.com/example/reference-service", map[string][][2]int{
		"internal/big":   {{180, 1}, {2, 0}},
		"internal/small": {{7, 1}, {3, 0}},
	})
	if problems := Build(load(t, atFloor), decl(90), nil).Problems(); len(problems) != 0 {
		t.Errorf("a package exactly at the floor failed: %v", problems)
	}
}

func TestExclusionLeavesThePackageOutOfTheDenominator(t *testing.T) {
	// cmd/ holds wiring only and is excluded, so its zero coverage must not
	// count against the figure or trip the per-package rule.
	text := profileText("github.com/example/reference-service", map[string][][2]int{
		"cmd/reference-service": {{40, 0}},
		"internal/a":            {{10, 1}},
	})
	set := load(t, text)

	without := Build(set, decl(90), nil)
	if len(without.Problems()) == 0 {
		t.Fatal("the fixture must fail without the exclusion, otherwise it proves nothing")
	}

	with := Build(set, decl(90, "cmd/"), nil)
	if problems := with.Problems(); len(problems) != 0 {
		t.Fatalf("the excluded package still failed the gate: %v", problems)
	}
	if _, total := with.Totals(); total != 10 {
		t.Errorf("denominator is %d statements, want 10", total)
	}
}

func TestExclusionFormsAreExplicit(t *testing.T) {
	d := decl(90, "cmd/", "internal/mock/generated")
	cases := map[string]bool{
		"cmd":                         true,
		"cmd/reference-service":       true,
		"cmd/reference-service/sub":   true,
		"cmdother":                    false,
		"internal/mock/generated":     true,
		"internal/mock/generated/sub": false,
		"internal/mock":               false,
	}
	for path, want := range cases {
		if got := d.IsExcluded(path); got != want {
			t.Errorf("IsExcluded(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestPackageWithNoTestsIsReportedMissing(t *testing.T) {
	text := profileText("github.com/example/reference-service", map[string][][2]int{
		"internal/a": {{10, 1}},
	})
	known := []string{"internal/a", "internal/b", "cmd/reference-service"}

	r := Build(load(t, text), decl(90, "cmd/"), known)
	problems := r.Problems()
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want one for the absent package: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "internal/b") {
		t.Errorf("problem %q does not name the absent package", problems[0])
	}
	if strings.Contains(problems[0], "cmd/reference-service") {
		t.Errorf("an excluded package was reported absent: %q", problems[0])
	}
}

func TestEmptyProfileFailsInsteadOfPassing(t *testing.T) {
	r := Build(load(t, "mode: set\n"), decl(90), nil)
	problems := r.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "did not run") {
		t.Fatalf("an empty profile did not fail as a gate that did not run: %v", problems)
	}
}

func TestRelativeTo(t *testing.T) {
	cases := map[string]string{
		"github.com/example/reference-service":            ".",
		"github.com/example/reference-service/internal/a": "internal/a",
		"other.example/pkg":                               "other.example/pkg",
	}
	for in, want := range cases {
		if got := relativeTo("github.com/example/reference-service", in); got != want {
			t.Errorf("relativeTo(%q) = %q, want %q", in, got, want)
		}
	}
	if got := relativeTo("", "any/path"); got != "any/path" {
		t.Errorf("relativeTo with no module = %q, want the import path unchanged", got)
	}
}

func TestReadDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".template.yaml")
	body := "module: github.com/example/reference-service\ncoverage:\n  threshold: 85\n  exclude:\n    - cmd/\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}

	d, found, err := ReadDeclaration(path)
	if err != nil || !found {
		t.Fatalf("ReadDeclaration = %v, found %t", err, found)
	}
	if d.Module != "github.com/example/reference-service" {
		t.Errorf("Module = %q", d.Module)
	}
	if d.Threshold() != 85 {
		t.Errorf("Threshold = %d, want 85", d.Threshold())
	}
	if !d.IsExcluded("cmd/reference-service") {
		t.Error("the declared exclusion did not apply")
	}
}

func TestReadDeclarationDefaults(t *testing.T) {
	d, found, err := ReadDeclaration(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("ReadDeclaration on a missing file: %v", err)
	}
	if found {
		t.Error("a missing file was reported as found")
	}
	if d.Threshold() != DefaultThreshold {
		t.Errorf("Threshold = %d, want the %d default", d.Threshold(), DefaultThreshold)
	}
}

func TestReadDeclarationHonoursAnExplicitZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".template.yaml")
	if err := os.WriteFile(path, []byte("coverage:\n  threshold: 0\n"), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}
	d, _, err := ReadDeclaration(path)
	if err != nil {
		t.Fatalf("ReadDeclaration: %v", err)
	}
	if d.Threshold() != 0 {
		t.Errorf("Threshold = %d, want the declared 0 rather than the default", d.Threshold())
	}
}

func TestReadDeclarationRejectsBrokenYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".template.yaml")
	if err := os.WriteFile(path, []byte("coverage:\n\tthreshold: 90\n"), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}
	if _, _, err := ReadDeclaration(path); err == nil {
		t.Fatal("a malformed declaration parsed without error")
	}
}

func TestWriteReportsEveryPackageAndTheOverall(t *testing.T) {
	text := profileText("github.com/example/reference-service", map[string][][2]int{
		"cmd/reference-service": {{4, 0}},
		"internal/a":            {{9, 1}, {1, 0}},
	})
	r := Build(load(t, text), decl(90, "cmd/"), nil)

	var b strings.Builder
	if err := r.Write(&b); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := b.String()
	for _, want := range []string{"internal/a", "cmd/reference-service", "excluded", "overall", "threshold 90%", "90.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

func TestZeroStatementPackageIsNotADivisionByZero(t *testing.T) {
	p := PackageCoverage{Path: "internal/empty"}
	if got := p.Percent(); got != 100 {
		t.Fatalf("Percent = %v for a package with no statements, want 100", got)
	}
	r := Report{Threshold: 90, Packages: []PackageCoverage{p}}
	if problems := r.Problems(); len(problems) != 1 || !strings.Contains(problems[0], "did not run") {
		t.Fatalf("problems = %v, want the empty-measurement failure only", problems)
	}
}
