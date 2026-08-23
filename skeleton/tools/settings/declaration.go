package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Declaration is the repository configuration this repository claims. It
// covers what makes the gates binding: which check must pass, who must review,
// how a branch merges, and which security features are on.
type Declaration struct {
	Repository Repository `yaml:"repository"`
	Security   Security   `yaml:"security"`
	Protection Protection `yaml:"protection"`
	CodeOwners CodeOwners `yaml:"codeowners"`
}

// Repository holds the merge and hygiene settings.
type Repository struct {
	DefaultBranch            string `yaml:"default_branch" json:"default_branch"`
	AllowSquashMerge         bool   `yaml:"allow_squash_merge" json:"allow_squash_merge"`
	AllowMergeCommit         bool   `yaml:"allow_merge_commit" json:"allow_merge_commit"`
	AllowRebaseMerge         bool   `yaml:"allow_rebase_merge" json:"allow_rebase_merge"`
	SquashMergeCommitTitle   string `yaml:"squash_merge_commit_title" json:"squash_merge_commit_title"`
	SquashMergeCommitMessage string `yaml:"squash_merge_commit_message" json:"squash_merge_commit_message"`
	DeleteBranchOnMerge      bool   `yaml:"delete_branch_on_merge" json:"delete_branch_on_merge"`
	AllowAutoMerge           bool   `yaml:"allow_auto_merge" json:"allow_auto_merge"`
	AllowUpdateBranch        bool   `yaml:"allow_update_branch" json:"allow_update_branch"`
}

// Security holds the scanning features. Their state is repository
// configuration, so a repository can carry every scanner in its pipeline and
// still have push protection off.
type Security struct {
	VulnerabilityAlerts          bool   `yaml:"vulnerability_alerts"`
	SecretScanning               string `yaml:"secret_scanning"`
	SecretScanningPushProtection string `yaml:"secret_scanning_push_protection"`
}

// Protection is the branch protection rule on the default branch.
type Protection struct {
	Branch               string `yaml:"branch"`
	RequiredStatusChecks struct {
		Strict   bool     `yaml:"strict"`
		Contexts []string `yaml:"contexts"`
	} `yaml:"required_status_checks"`
	RequiredPullRequestReviews struct {
		RequiredApprovingReviewCount int  `yaml:"required_approving_review_count"`
		DismissStaleReviews          bool `yaml:"dismiss_stale_reviews"`
		RequireCodeOwnerReviews      bool `yaml:"require_code_owner_reviews"`
	} `yaml:"required_pull_request_reviews"`
	RequiredLinearHistory          bool `yaml:"required_linear_history"`
	RequiredConversationResolution bool `yaml:"required_conversation_resolution"`
	AllowForcePushes               bool `yaml:"allow_force_pushes"`
	AllowDeletions                 bool `yaml:"allow_deletions"`
	EnforceAdmins                  bool `yaml:"enforce_admins"`
}

// CodeOwners names the paths that must have a reviewer.
type CodeOwners struct {
	RequiredPaths []string `yaml:"required_paths"`
}

// LoadDeclaration reads and validates the declaration at path. Unknown fields
// are an error, because a setting nobody applies is worse than an absent one:
// it reads as configured.
func LoadDeclaration(path string) (Declaration, error) {
	var d Declaration
	data, err := os.ReadFile(path)
	if err != nil {
		return d, fmt.Errorf("read %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&d); err != nil {
		return d, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := d.Validate(); err != nil {
		return d, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// Validate rejects a declaration that cannot bind anything.
func (d Declaration) Validate() error {
	if d.Repository.DefaultBranch == "" {
		return fmt.Errorf("repository.default_branch is required")
	}
	if d.Protection.Branch == "" {
		return fmt.Errorf("protection.branch is required")
	}
	if len(d.Protection.RequiredStatusChecks.Contexts) == 0 {
		return fmt.Errorf("protection.required_status_checks.contexts is empty, " +
			"so no check is required and the pipeline is advisory")
	}
	if !d.Repository.AllowSquashMerge {
		return fmt.Errorf("repository.allow_squash_merge is off, " +
			"so no merge method is available for the commit convention the release version reads")
	}
	switch d.Security.SecretScanning {
	case "enabled", "disabled":
	default:
		return fmt.Errorf("security.secret_scanning is %q, want enabled or disabled",
			d.Security.SecretScanning)
	}
	switch d.Security.SecretScanningPushProtection {
	case "enabled", "disabled":
	default:
		return fmt.Errorf("security.secret_scanning_push_protection is %q, want enabled or disabled",
			d.Security.SecretScanningPushProtection)
	}
	return nil
}
