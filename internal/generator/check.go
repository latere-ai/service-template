package generator

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Exit codes. The contract fixes 3 and 4 so that a pipeline can print the
// right instruction without parsing output: 3 means revert the edit or send it
// upstream, 4 means run template upgrade. Every other failure is 1, because it
// means the check could not be evaluated at all.
const (
	ExitOK     = 0
	ExitError  = 1
	ExitEdited = 3
	ExitBehind = 4
)

// Verdict is the state of one checked file.
type Verdict string

const (
	// VerdictClean means the file matches the template and the lock.
	VerdictClean Verdict = "clean"
	// VerdictEdited means the repository changed a generated file.
	VerdictEdited Verdict = "edited"
	// VerdictBehind means the template changed and the repository has not
	// absorbed it.
	VerdictBehind Verdict = "behind"
	// VerdictWaived means the file is edited and a live waiver covers it.
	VerdictWaived Verdict = "waived"
	// VerdictMalformed means the file could not be compared, for example a
	// merged file whose markers are missing.
	VerdictMalformed Verdict = "malformed"
)

// FileVerdict is the outcome for one checked path.
type FileVerdict struct {
	Path    string
	Verdict Verdict
	Detail  string
	Diff    string
	Waiver  *Waiver
}

// CheckReport is the whole outcome of a drift check.
type CheckReport struct {
	Profile  string
	Version  string
	Files    []FileVerdict
	Expired  []Waiver
	Warnings []string
}

// Exit maps the report to the process exit code. An edited file outranks a
// behind file: the contract puts a repository that is both at code 3, because
// the local edit has to be resolved before an upgrade can land.
func (r *CheckReport) Exit() int {
	code := ExitOK
	if len(r.Expired) > 0 {
		return ExitError
	}
	for _, f := range r.Files {
		switch f.Verdict {
		case VerdictMalformed:
			return ExitError
		case VerdictEdited:
			code = ExitEdited
		case VerdictBehind:
			if code == ExitOK {
				code = ExitBehind
			}
		}
	}
	return code
}

// Check compares the repository against the pinned template. It reads nothing
// it does not need and writes nothing at all.
func Check(src fs.FS, dir string, cfg *Config, lock *Lock, now time.Time) (*CheckReport, error) {
	if err := guardProfile(cfg, lock); err != nil {
		return nil, err
	}
	plan, err := BuildPlan(src, cfg)
	if err != nil {
		return nil, err
	}
	report := &CheckReport{Profile: cfg.Profile, Version: cfg.Version}

	waived := map[string]Waiver{}
	for _, w := range cfg.Waivers {
		if !w.Expires.After(now) {
			report.Expired = append(report.Expired, w)
			continue
		}
		waived[w.Path] = w
	}

	selected := map[string]bool{}
	for _, pf := range plan.Files {
		selected[pf.Target] = true
		if pf.Entry.Mode == ModeSeed {
			continue
		}
		v := checkFile(dir, pf, lock)
		if w, ok := waived[v.Path]; ok && v.Verdict == VerdictEdited {
			hit := w
			v.Verdict = VerdictWaived
			v.Waiver = &hit
		}
		report.Files = append(report.Files, v)
	}

	for _, e := range lock.Files {
		if selected[e.Path] || e.Mode == ModeSeed {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(e.Path))); err != nil {
			continue
		}
		report.Files = append(report.Files, FileVerdict{
			Path:    e.Path,
			Verdict: VerdictBehind,
			Detail:  "the declaration no longer selects this file; template sync removes it",
		})
	}

	for _, w := range cfg.Waivers {
		if !selected[w.Path] {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("the waiver for %q covers no generated file", w.Path))
		}
	}

	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })
	sort.Strings(report.Warnings)
	return report, nil
}

// checkFile applies the contract's two axes: whether the repository changed
// the file since the generator wrote it, and whether the template moved since
// the lock was written.
func checkFile(dir string, pf PlanFile, lock *Lock) FileVerdict {
	full := filepath.Join(dir, filepath.FromSlash(pf.Target))
	disk, readErr := os.ReadFile(full)
	locked := ""
	if e, ok := lock.Entry(pf.Target); ok {
		locked = e.Digest
	}
	want := pf.Digest

	have := ""
	var haveContent []byte
	switch {
	case os.IsNotExist(readErr):
		// A missing file has no content. When the lock has no record either,
		// the template added the file and the repository is behind.
	case readErr != nil:
		return FileVerdict{Path: pf.Target, Verdict: VerdictMalformed,
			Detail: fmt.Sprintf("cannot read the file: %v", readErr)}
	case pf.Entry.Mode == ModeMerged:
		region, err := SplitRegion(pf.Target, disk)
		if err != nil {
			return FileVerdict{Path: pf.Target, Verdict: VerdictMalformed, Detail: err.Error()}
		}
		haveContent = region.Content()
		have = Digest(haveContent)
	default:
		haveContent = disk
		have = Digest(disk)
	}

	wantContent := pf.Content
	if pf.Entry.Mode == ModeMerged {
		wantContent = Region{Body: pf.Body}.Content()
	}

	switch {
	case have == want && locked == want:
		return FileVerdict{Path: pf.Target, Verdict: VerdictClean}
	case have == want:
		return FileVerdict{Path: pf.Target, Verdict: VerdictBehind,
			Detail: "the file matches the template and the lock records a different digest; run template sync"}
	case have == locked:
		detail := "the template changed this file; run template upgrade"
		if have == "" {
			detail = "the template added this file; run template upgrade"
		}
		return FileVerdict{Path: pf.Target, Verdict: VerdictBehind, Detail: detail,
			Diff: UnifiedDiff(pf.Target, wantContent, haveContent)}
	default:
		detail := "the repository changed this file"
		if have == "" {
			detail = "the repository deleted this file"
		}
		return FileVerdict{Path: pf.Target, Verdict: VerdictEdited, Detail: detail,
			Diff: UnifiedDiff(pf.Target, wantContent, haveContent)}
	}
}

// String renders the report the way the check command prints it.
func (r *CheckReport) String() string {
	var b strings.Builder
	counts := map[Verdict]int{}
	for _, f := range r.Files {
		counts[f.Verdict]++
	}
	for _, w := range r.Expired {
		fmt.Fprintf(&b, "expired waiver: %s expired on %s (%s)\n",
			w.Path, w.Expires.Format("2006-01-02"), w.Reason)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	for _, f := range r.Files {
		switch f.Verdict {
		case VerdictClean:
			continue
		case VerdictWaived:
			fmt.Fprintf(&b, "waived: %s until %s (%s)\n",
				f.Path, f.Waiver.Expires.Format("2006-01-02"), f.Waiver.Reason)
		default:
			fmt.Fprintf(&b, "%s: %s: %s\n", f.Verdict, f.Path, f.Detail)
			if f.Diff != "" {
				b.WriteString(indent(f.Diff))
			}
		}
	}
	fmt.Fprintf(&b, "%d files checked: %d clean, %d edited, %d behind, %d waived\n",
		len(r.Files), counts[VerdictClean], counts[VerdictEdited], counts[VerdictBehind], counts[VerdictWaived])
	switch r.Exit() {
	case ExitEdited:
		b.WriteString("the repository edited generated files; revert the change or send it upstream\n")
	case ExitBehind:
		b.WriteString("the repository is behind the template; run template upgrade\n")
	case ExitError:
		b.WriteString("the check could not be evaluated\n")
	}
	return b.String()
}

func indent(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}
