package main

import (
	"fmt"
	"strings"
)

// Evidence renders the markdown block the release carries. It names every
// assertion, the value the target actually returned, and the number of
// attempts the value took, so a check that passed on its last attempt is
// visible rather than hidden behind a pass mark.
func Evidence(cfg Config, results []Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Live check: %s\n\n", cfg.Target)
	fmt.Fprintf(&b, "Address: `%s`  \n", cfg.BaseURL)
	fmt.Fprintf(&b, "Retry window: %s, first backoff %s\n\n", cfg.Window, cfg.Backoff)

	b.WriteString("| Assertion | Expected | Observed | Attempts | Result |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range results {
		outcome := "pass"
		if !r.OK() {
			outcome = "fail: " + r.Err.Error()
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s |\n",
			cell(r.Name), cell(r.Expected), cell(r.Observed), r.Attempts, cell(outcome))
	}

	failed := Failures(results)
	fmt.Fprintf(&b, "\n%d of %d assertions passed.\n", len(results)-len(failed), len(results))
	return b.String()
}

// cell makes a value safe for a markdown table cell: one line, with the column
// separator escaped. An observed value that breaks the table is an observed
// value nobody reads.
func cell(s string) string {
	if s == "" {
		return "(empty)"
	}
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", "\\|")
}
