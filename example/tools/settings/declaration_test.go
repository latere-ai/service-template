package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The declaration this repository ships must state a binding configuration:
// squash only, an owner review, and the gate as a required check.
func TestShippedDeclaration(t *testing.T) {
	d, err := LoadDeclaration(filepath.Join("..", "..", ".github", "settings.yml"))
	if err != nil {
		t.Fatalf("load the declaration: %v", err)
	}
	if !d.Repository.AllowSquashMerge || d.Repository.AllowMergeCommit || d.Repository.AllowRebaseMerge {
		t.Error("the merge method must be squash only, because the release version reads the commit subjects")
	}
	if d.Repository.SquashMergeCommitTitle != "PR_TITLE" {
		t.Error("the squash subject must be the pull request title")
	}
	if !d.Protection.RequiredPullRequestReviews.RequireCodeOwnerReviews {
		t.Error("owner review must be required, or ownership guards nothing")
	}
	if d.Protection.RequiredPullRequestReviews.RequiredApprovingReviewCount < 1 {
		t.Error("at least one approval must be required")
	}
	if !d.Protection.RequiredStatusChecks.Strict {
		t.Error("the branch must be up to date before merge, or the gate judged a different tree")
	}
	if !d.Protection.RequiredLinearHistory || d.Protection.AllowForcePushes || d.Protection.AllowDeletions {
		t.Error("the default branch must keep a linear history and refuse force pushes and deletion")
	}
	if d.Security.SecretScanningPushProtection != "enabled" {
		t.Error("push protection must be on")
	}
	found := false
	for _, context := range d.Protection.RequiredStatusChecks.Contexts {
		if strings.HasSuffix(context, "/ gate") {
			found = true
		}
	}
	if !found {
		t.Errorf("no required context names the gate job: %v", d.Protection.RequiredStatusChecks.Contexts)
	}
}

// A declaration that cannot bind anything is rejected where it is read, not
// after it has been applied.
func TestDeclarationValidation(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "no required context",
			content: `repository:
  default_branch: main
  allow_squash_merge: true
security:
  secret_scanning: enabled
  secret_scanning_push_protection: enabled
protection:
  branch: main
  required_status_checks:
    strict: true
    contexts: []
`,
			wantErr: "advisory",
		},
		{
			name: "no squash merge",
			content: `repository:
  default_branch: main
  allow_squash_merge: false
security:
  secret_scanning: enabled
  secret_scanning_push_protection: enabled
protection:
  branch: main
  required_status_checks:
    strict: true
    contexts: [verify / gate]
`,
			wantErr: "allow_squash_merge",
		},
		{
			name: "an unknown field",
			content: `repository:
  default_branch: main
  allow_squash_merge: true
  allow_squashh_merge: true
`,
			wantErr: "field allow_squashh_merge",
		},
		{
			name: "an unknown secret scanning state",
			content: `repository:
  default_branch: main
  allow_squash_merge: true
security:
  secret_scanning: on
  secret_scanning_push_protection: enabled
protection:
  branch: main
  required_status_checks:
    strict: true
    contexts: [verify / gate]
`,
			wantErr: "secret_scanning",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeTemp(t, "settings.yml", c.content)
			_, err := LoadDeclaration(path)
			if err == nil {
				t.Fatalf("the declaration was accepted")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("the failure does not mention %q: %v", c.wantErr, err)
			}
		})
	}
}
