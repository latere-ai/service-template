package store

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The rolling-deploy rule this file enforces.
//
// Migrations apply before the new code starts, and the previous release keeps
// serving until the rollout finishes. Anything the previous schema exposed is
// therefore still being read while the new schema is live, so a migration that
// removes a column or a table breaks the release that is still running.
//
// A rename is three releases: add the new column and write both, backfill and
// read the new one, then drop the old one. The third release is the only place
// a removal is safe, and it is the only place a waiver belongs.

// waiverPrefix introduces the annotation that permits one removal. It is a
// fixed token rather than free-form prose so a reviewer can grep for every
// removal a repository has ever allowed.
const waiverPrefix = "template:allow-drop"

// waiverLine matches the annotation:
//
//	-- template:allow-drop users.legacy_email since=v1.4.0 reason=the last reader shipped in v1.4.0
//
// since names the release that stopped reading the target, and reason states
// what makes the removal safe. Both are required, because a waiver with no
// stated reader history is a removal nobody checked.
var waiverLine = regexp.MustCompile(
	`(?i)^\s*--\s*` + regexp.QuoteMeta(waiverPrefix) +
		`\s+([^\s]+)\s+since=([^\s]+)\s+reason=(\S.*?)\s*$`)

// waiver is one permitted removal.
type waiver struct {
	target string
	since  string
	reason string
	line   int
	used   bool
}

// removal is a destructive statement found in a migration.
type removal struct {
	// target is "table" for a table removal and "table.column" for a column
	// removal. It is the identifier a waiver must name.
	target string
	// what describes the statement in the failure message.
	what string
}

// CheckCompatibility reports every migration that removes something the
// previous release can still read.
//
// The baseline for migration N is the schema produced by migrations 1 to N-1,
// which is exactly the schema the running release was built against. A removal
// against that baseline fails unless the migration carries a waiver naming the
// same target.
func CheckCompatibility(migs []Migration) error {
	sc := newSchema()
	var problems []error

	for _, m := range migs {
		waivers, err := parseWaivers(m)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		// The baseline is the schema as the previous release saw it, so a
		// table created and removed inside one migration is not a break.
		baseline := sc.clone()
		stmts, err := statements(m.SQL)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", m.Path, err))
			continue
		}
		for _, st := range stmts {
			removals, err := apply(sc, st)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", m.Path, err))
				continue
			}
			for _, r := range removals {
				if !baseline.has(r.target) {
					continue
				}
				w := findWaiver(waivers, r.target)
				if w == nil {
					problems = append(problems, fmt.Errorf(
						"%s: %s removes %q, which the previous release still reads; "+
							"split the change into add, backfill, and remove, or record the removal with "+
							"-- %s %s since=<release> reason=<why no reader is left>",
						m.Path, r.what, r.target, waiverPrefix, r.target))
					continue
				}
				w.used = true
			}
		}
		for _, w := range waivers {
			if !w.used {
				problems = append(problems, fmt.Errorf(
					"%s:%d: the waiver for %q matches no removal in this migration",
					m.Path, w.line, w.target))
			}
		}
	}
	return errors.Join(problems...)
}

// findWaiver returns the waiver naming target, or nil.
func findWaiver(ws []*waiver, target string) *waiver {
	for _, w := range ws {
		if w.target == target {
			return w
		}
	}
	return nil
}

// parseWaivers reads the annotations from a migration. A line that opens with
// the token but does not carry both fields is an error, so a malformed waiver
// fails the check instead of silently permitting nothing.
func parseWaivers(m Migration) ([]*waiver, error) {
	var out []*waiver
	var problems []error
	for i, line := range strings.Split(m.SQL, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "--") || !strings.Contains(strings.ToLower(trimmed), waiverPrefix) {
			continue
		}
		f := waiverLine.FindStringSubmatch(line)
		if f == nil {
			problems = append(problems, fmt.Errorf(
				"%s:%d: a waiver is written -- %s <table>[.<column>] since=<release> reason=<why>",
				m.Path, i+1, waiverPrefix))
			continue
		}
		out = append(out, &waiver{
			target: normalizeTarget(f[1]),
			since:  f[2],
			reason: f[3],
			line:   i + 1,
		})
	}
	return out, errors.Join(problems...)
}

// normalizeTarget folds a waiver target the way the parser folds identifiers,
// so the annotation and the statement compare equal.
func normalizeTarget(s string) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = normalizeIdent(p)
	}
	if len(parts) == 3 {
		// A schema-qualified column keeps only the table and the column, which
		// is how the parser stores it.
		parts = parts[1:]
	}
	return strings.Join(parts, ".")
}
