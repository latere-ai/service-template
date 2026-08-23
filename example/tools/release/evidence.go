package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Evidence is the block the published release carries. It is machine
// generated from the run. A field that cannot be filled fails the release
// rather than printing "unknown", because an evidence block with gaps is worse
// than none: it looks like proof.
type Evidence struct {
	// Version is the tag being released.
	Version string `json:"version"`
	// Commit is the full commit the tag points at.
	Commit string `json:"commit"`
	// VerifyRun is the run that passed that exact commit.
	VerifyRun string `json:"verify_run"`
	// Image is the registry reference pinned by digest.
	Image string `json:"image"`
	// Architectures are the platforms in the published manifest list.
	Architectures string `json:"architectures"`
	// SBOM, Provenance, and Signature record the result of verifying each
	// attestation after the push, not the fact that one was produced.
	SBOM       string `json:"sbom"`
	Provenance string `json:"provenance"`
	Signature  string `json:"signature"`
	// Scan is the image vulnerability scan result.
	Scan string `json:"scan"`
	// Target is the deployment target the release rolled out to.
	Target string `json:"target"`
	// RolloutCompleted is when the rollout finished.
	RolloutCompleted string `json:"rollout_completed"`
	// Replicas is the ready replica count after the rollout.
	Replicas string `json:"replicas"`
	// The build identity the live service reported.
	LiveVersion string `json:"live_version"`
	LiveCommit  string `json:"live_commit"`
	LiveAsset   string `json:"live_asset"`
	// LiveCheck is the smoke evidence block, one row per assertion.
	LiveCheck string `json:"live_check"`
}

// placeholders are values that look filled and are not. A release that prints
// one of these has the gap the evidence block exists to prevent.
// "none" is deliberately absent: it is the entry asset a service with no
// frontend declares, which is a statement rather than a gap.
var placeholders = map[string]bool{
	"unknown": true,
	"null":    true,
	"n/a":     true,
	"-":       true,
	"tbd":     true,
}

// fields lists every field with the name it carries in the block, in the order
// the block prints them.
func (e Evidence) fields() []struct{ Name, Value string } {
	return []struct{ Name, Value string }{
		{"version", e.Version},
		{"commit", e.Commit},
		{"verify run", e.VerifyRun},
		{"image", e.Image},
		{"architectures", e.Architectures},
		{"bill of materials", e.SBOM},
		{"provenance", e.Provenance},
		{"signature", e.Signature},
		{"image scan", e.Scan},
		{"target", e.Target},
		{"rollout completed", e.RolloutCompleted},
		{"ready replicas", e.Replicas},
		{"live version", e.LiveVersion},
		{"live commit", e.LiveCommit},
		{"live entry asset", e.LiveAsset},
		{"live check", e.LiveCheck},
	}
}

// Validate reports every field that is empty or holds a placeholder, and every
// disagreement between the release and what the live service reported. A
// release that says it shipped a version the service is not serving is the
// exact claim the block exists to make checkable.
func (e Evidence) Validate() error {
	var problems []string
	for _, f := range e.fields() {
		v := strings.TrimSpace(f.Value)
		if v == "" {
			problems = append(problems, fmt.Sprintf("%s is empty", f.Name))
			continue
		}
		if placeholders[strings.ToLower(v)] {
			problems = append(problems, fmt.Sprintf("%s is the placeholder %q", f.Name, v))
		}
	}
	if e.LiveVersion != "" && e.Version != "" && e.LiveVersion != e.Version {
		problems = append(problems,
			fmt.Sprintf("live version %q is not the released version %q", e.LiveVersion, e.Version))
	}
	if e.LiveCommit != "" && e.Commit != "" && !strings.EqualFold(e.LiveCommit, e.Commit) {
		problems = append(problems,
			fmt.Sprintf("live commit %q is not the tagged commit %q", e.LiveCommit, e.Commit))
	}
	if !strings.Contains(e.Image, "@sha256:") {
		problems = append(problems,
			fmt.Sprintf("image %q is not pinned by digest", e.Image))
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("the evidence block is incomplete:\n  %s", strings.Join(problems, "\n  "))
}

// Markdown renders the block appended to the generated release notes.
func (e Evidence) Markdown() string {
	var b strings.Builder
	b.WriteString("## Release evidence\n\n")
	b.WriteString("| Field | Value |\n| --- | --- |\n")
	rows := []struct{ name, value string }{
		{"Commit", fmt.Sprintf("`%s`, verified by %s", e.Commit, e.VerifyRun)},
		{"Image", fmt.Sprintf("`%s` (%s)", e.Image, e.Architectures)},
		{"Bill of materials", e.SBOM},
		{"Provenance", e.Provenance},
		{"Signature", e.Signature},
		{"Image scan", e.Scan},
		{"Deploy", fmt.Sprintf("%s, completed %s, %s ready replicas", e.Target, e.RolloutCompleted, e.Replicas)},
		{"Build identity", fmt.Sprintf("version `%s`, commit `%s`, entry asset `%s`", e.LiveVersion, e.LiveCommit, e.LiveAsset)},
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s |\n", row.name, oneLine(row.value))
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(e.LiveCheck, "\n"))
	b.WriteString("\n")
	return b.String()
}

// oneLine keeps a table cell a table cell.
func oneLine(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", "\\|")
}

// ParseEvidence reads the block the pipeline assembled from its job outputs.
func ParseEvidence(data []byte) (Evidence, error) {
	var e Evidence
	if err := json.Unmarshal(data, &e); err != nil {
		return Evidence{}, fmt.Errorf("parse evidence: %w", err)
	}
	return e, nil
}
