package main

import (
	"strings"
	"testing"
)

// complete is an evidence block with every field filled from a run.
func complete() Evidence {
	return Evidence{
		Version:          "v1.4.0",
		Commit:           "1111111111111111111111111111111111111111",
		VerifyRun:        "https://example.test/runs/42",
		Image:            "ghcr.io/owner/service@sha256:" + strings.Repeat("a", 64),
		Architectures:    "linux/amd64, linux/arm64",
		SBOM:             "verified, 214 components",
		Provenance:       "verified against the release workflow",
		Signature:        "verified, keyless certificate for refs/tags/v1.4.0",
		Scan:             "0 critical, 0 high",
		Target:           "production",
		RolloutCompleted: "2026-06-01T10:04:11Z",
		Replicas:         "3",
		LiveVersion:      "v1.4.0",
		LiveCommit:       "1111111111111111111111111111111111111111",
		LiveAsset:        "index-C3xK9pQ2.js",
		LiveCheck:        "### Live check: production\n\n| Assertion | Observed |\n",
	}
}

func TestCompleteEvidencePasses(t *testing.T) {
	if err := complete().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// An evidence block with gaps is worse than none: it looks like proof.
func TestEveryMissingFieldFailsTheRelease(t *testing.T) {
	blank := func(e *Evidence, name string) {
		switch name {
		case "version":
			e.Version = ""
		case "commit":
			e.Commit = ""
		case "verify run":
			e.VerifyRun = ""
		case "image":
			e.Image = ""
		case "architectures":
			e.Architectures = ""
		case "bill of materials":
			e.SBOM = ""
		case "provenance":
			e.Provenance = ""
		case "signature":
			e.Signature = ""
		case "image scan":
			e.Scan = ""
		case "target":
			e.Target = ""
		case "rollout completed":
			e.RolloutCompleted = ""
		case "ready replicas":
			e.Replicas = ""
		case "live version":
			e.LiveVersion = ""
		case "live commit":
			e.LiveCommit = ""
		case "live entry asset":
			e.LiveAsset = ""
		case "live check":
			e.LiveCheck = ""
		default:
			t.Fatalf("no way to blank %q", name)
		}
	}
	for _, field := range complete().fields() {
		t.Run(field.Name, func(t *testing.T) {
			e := complete()
			blank(&e, field.Name)
			err := e.Validate()
			if err == nil {
				t.Fatalf("Validate accepted a block with no %s", field.Name)
			}
			mustContain(t, err.Error(), field.Name, "the failure")
		})
	}
}

// A placeholder is a gap wearing a value.
func TestPlaceholderValuesFailTheRelease(t *testing.T) {
	for _, value := range []string{"unknown", "UNKNOWN", "null", "n/a", "-", "tbd"} {
		e := complete()
		e.Provenance = value
		if err := e.Validate(); err == nil {
			t.Errorf("Validate accepted the placeholder %q", value)
		}
	}
}

// The release claims a version is serving, so the claim is checked against
// what the live service actually reported.
func TestEvidenceRejectsALiveIdentityThatDisagrees(t *testing.T) {
	e := complete()
	e.LiveVersion = "v1.3.9"
	err := e.Validate()
	if err == nil {
		t.Fatal("Validate accepted a release whose live version is another build")
	}
	mustContain(t, err.Error(), "not the released version", "the failure")

	e = complete()
	e.LiveCommit = strings.Repeat("2", 40)
	if err := e.Validate(); err == nil {
		t.Fatal("Validate accepted a release whose live commit is another commit")
	}
}

func TestEvidenceRequiresADigestPinnedImage(t *testing.T) {
	e := complete()
	e.Image = "ghcr.io/owner/service:v1.4.0"
	err := e.Validate()
	if err == nil {
		t.Fatal("Validate accepted an image pinned by tag")
	}
	mustContain(t, err.Error(), "not pinned by digest", "the failure")
}

func TestEvidenceMarkdownCarriesEveryField(t *testing.T) {
	block := complete().Markdown()
	for _, want := range []string{
		"## Release evidence",
		"1111111111111111111111111111111111111111",
		"https://example.test/runs/42",
		"sha256:",
		"linux/arm64",
		"214 components",
		"keyless certificate",
		"0 critical, 0 high",
		"production, completed 2026-06-01T10:04:11Z, 3 ready replicas",
		"index-C3xK9pQ2.js",
		"### Live check: production",
	} {
		mustContain(t, block, want, "the evidence block")
	}
}

func TestParseEvidence(t *testing.T) {
	e, err := ParseEvidence([]byte(`{"version":"v1.0.0","image":"ghcr.io/owner/service@sha256:abc"}`))
	if err != nil {
		t.Fatalf("ParseEvidence: %v", err)
	}
	if e.Version != "v1.0.0" {
		t.Errorf("evidence = %+v", e)
	}
	if _, err := ParseEvidence([]byte("not json")); err == nil {
		t.Fatal("ParseEvidence accepted content it could not read")
	}
}

// A service with no frontend declares that, and the declaration is not a gap.
// Rejecting it would fail every release of a repository that ships no bundle.
func TestNoFrontendEntryAssetIsAccepted(t *testing.T) {
	e := complete()
	e.LiveAsset = "none"
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate rejected a frontend-less release: %v", err)
	}
	mustContain(t, e.Markdown(), "entry asset `none`", "the evidence block")
}
