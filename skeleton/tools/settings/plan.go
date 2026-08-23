package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Areas group the settings by the API call that carries them.
const (
	AreaRepository = "repository"
	AreaSecurity   = "security"
	AreaProtection = "protection"
)

// Change is one setting whose live value differs from the declaration.
type Change struct {
	Area     string
	Setting  string
	Live     string
	Declared string
}

// Live is the repository configuration as the API reports it.
type Live struct {
	Repository          repositoryState
	Protection          *protectionState
	VulnerabilityAlerts bool
	// ProtectionFound is false when the branch carries no protection rule at
	// all, which is a different report than a rule with the wrong values.
	ProtectionFound bool
}

type repositoryState struct {
	DefaultBranch            string `json:"default_branch"`
	AllowSquashMerge         bool   `json:"allow_squash_merge"`
	AllowMergeCommit         bool   `json:"allow_merge_commit"`
	AllowRebaseMerge         bool   `json:"allow_rebase_merge"`
	SquashMergeCommitTitle   string `json:"squash_merge_commit_title"`
	SquashMergeCommitMessage string `json:"squash_merge_commit_message"`
	DeleteBranchOnMerge      bool   `json:"delete_branch_on_merge"`
	AllowAutoMerge           bool   `json:"allow_auto_merge"`
	AllowUpdateBranch        bool   `json:"allow_update_branch"`
	SecurityAndAnalysis      *struct {
		SecretScanning struct {
			Status string `json:"status"`
		} `json:"secret_scanning"`
		SecretScanningPushProtection struct {
			Status string `json:"status"`
		} `json:"secret_scanning_push_protection"`
	} `json:"security_and_analysis"`
}

type enabledFlag struct {
	Enabled bool `json:"enabled"`
}

type protectionState struct {
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
	} `json:"required_status_checks"`
	RequiredPullRequestReviews *struct {
		DismissStaleReviews          bool `json:"dismiss_stale_reviews"`
		RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
		RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	} `json:"required_pull_request_reviews"`
	RequiredLinearHistory          enabledFlag `json:"required_linear_history"`
	RequiredConversationResolution enabledFlag `json:"required_conversation_resolution"`
	AllowForcePushes               enabledFlag `json:"allow_force_pushes"`
	AllowDeletions                 enabledFlag `json:"allow_deletions"`
	EnforceAdmins                  enabledFlag `json:"enforce_admins"`
}

// Fetch reads the live configuration. Every mode reads before it decides, so
// an apply changes only what differs and a report states what differs.
func Fetch(ctx context.Context, c *Client, d Declaration) (Live, error) {
	var live Live
	if _, err := c.Get(ctx, c.repoPath(""), &live.Repository); err != nil {
		return live, err
	}

	status, err := c.Get(ctx, c.repoPath("/vulnerability-alerts"), nil)
	if err != nil {
		return live, err
	}
	live.VulnerabilityAlerts = status == http.StatusNoContent

	var protection protectionState
	status, err = c.Get(ctx, c.repoPath("/branches/"+d.Protection.Branch+"/protection"), &protection)
	if err != nil {
		return live, err
	}
	if status != http.StatusNotFound {
		live.Protection = &protection
		live.ProtectionFound = true
	}
	return live, nil
}

// Diff states every setting whose live value differs from the declaration, in
// a fixed order so two runs produce the same report.
func Diff(d Declaration, live Live) []Change {
	var changes []Change
	add := func(area, setting, liveValue, declared string) {
		if liveValue != declared {
			changes = append(changes, Change{Area: area, Setting: setting, Live: liveValue, Declared: declared})
		}
	}

	r, want := live.Repository, d.Repository
	add(AreaRepository, "default_branch", r.DefaultBranch, want.DefaultBranch)
	add(AreaRepository, "allow_squash_merge", yesNo(r.AllowSquashMerge), yesNo(want.AllowSquashMerge))
	add(AreaRepository, "allow_merge_commit", yesNo(r.AllowMergeCommit), yesNo(want.AllowMergeCommit))
	add(AreaRepository, "allow_rebase_merge", yesNo(r.AllowRebaseMerge), yesNo(want.AllowRebaseMerge))
	add(AreaRepository, "squash_merge_commit_title", r.SquashMergeCommitTitle, want.SquashMergeCommitTitle)
	add(AreaRepository, "squash_merge_commit_message", r.SquashMergeCommitMessage, want.SquashMergeCommitMessage)
	add(AreaRepository, "delete_branch_on_merge", yesNo(r.DeleteBranchOnMerge), yesNo(want.DeleteBranchOnMerge))
	add(AreaRepository, "allow_auto_merge", yesNo(r.AllowAutoMerge), yesNo(want.AllowAutoMerge))
	add(AreaRepository, "allow_update_branch", yesNo(r.AllowUpdateBranch), yesNo(want.AllowUpdateBranch))

	add(AreaSecurity, "vulnerability_alerts", yesNo(live.VulnerabilityAlerts), yesNo(d.Security.VulnerabilityAlerts))
	secretScanning, pushProtection := "disabled", "disabled"
	if s := r.SecurityAndAnalysis; s != nil {
		if s.SecretScanning.Status != "" {
			secretScanning = s.SecretScanning.Status
		}
		if s.SecretScanningPushProtection.Status != "" {
			pushProtection = s.SecretScanningPushProtection.Status
		}
	}
	add(AreaSecurity, "secret_scanning", secretScanning, d.Security.SecretScanning)
	add(AreaSecurity, "secret_scanning_push_protection", pushProtection, d.Security.SecretScanningPushProtection)

	p := d.Protection
	if !live.ProtectionFound {
		changes = append(changes, Change{
			Area:     AreaProtection,
			Setting:  "branch " + p.Branch,
			Live:     "unprotected",
			Declared: "protected",
		})
		return changes
	}

	l := live.Protection
	liveStrict, liveContexts := "no", ""
	if l.RequiredStatusChecks != nil {
		liveStrict = yesNo(l.RequiredStatusChecks.Strict)
		liveContexts = joinSorted(l.RequiredStatusChecks.Contexts)
	}
	add(AreaProtection, "required_status_checks.strict", liveStrict, yesNo(p.RequiredStatusChecks.Strict))
	add(AreaProtection, "required_status_checks.contexts", liveContexts, joinSorted(p.RequiredStatusChecks.Contexts))

	liveCount, liveDismiss, liveOwners := "0", "no", "no"
	if l.RequiredPullRequestReviews != nil {
		liveCount = strconv.Itoa(l.RequiredPullRequestReviews.RequiredApprovingReviewCount)
		liveDismiss = yesNo(l.RequiredPullRequestReviews.DismissStaleReviews)
		liveOwners = yesNo(l.RequiredPullRequestReviews.RequireCodeOwnerReviews)
	}
	add(AreaProtection, "required_approving_review_count", liveCount,
		strconv.Itoa(p.RequiredPullRequestReviews.RequiredApprovingReviewCount))
	add(AreaProtection, "dismiss_stale_reviews", liveDismiss, yesNo(p.RequiredPullRequestReviews.DismissStaleReviews))
	add(AreaProtection, "require_code_owner_reviews", liveOwners, yesNo(p.RequiredPullRequestReviews.RequireCodeOwnerReviews))
	add(AreaProtection, "required_linear_history", yesNo(l.RequiredLinearHistory.Enabled), yesNo(p.RequiredLinearHistory))
	add(AreaProtection, "required_conversation_resolution", yesNo(l.RequiredConversationResolution.Enabled),
		yesNo(p.RequiredConversationResolution))
	add(AreaProtection, "allow_force_pushes", yesNo(l.AllowForcePushes.Enabled), yesNo(p.AllowForcePushes))
	add(AreaProtection, "allow_deletions", yesNo(l.AllowDeletions.Enabled), yesNo(p.AllowDeletions))
	add(AreaProtection, "enforce_admins", yesNo(l.EnforceAdmins.Enabled), yesNo(p.EnforceAdmins))

	return changes
}

// MissingContexts names the declared required checks the live protection does
// not require. A repository whose gate is not required merges a red branch.
func MissingContexts(d Declaration, live Live) []string {
	required := map[string]bool{}
	if live.ProtectionFound && live.Protection.RequiredStatusChecks != nil {
		for _, context := range live.Protection.RequiredStatusChecks.Contexts {
			required[context] = true
		}
	}
	var missing []string
	for _, context := range d.Protection.RequiredStatusChecks.Contexts {
		if !required[context] {
			missing = append(missing, context)
		}
	}
	return missing
}

// WriteChanges renders the difference between the declaration and the live
// configuration.
func WriteChanges(w io.Writer, changes []Change) error {
	if len(changes) == 0 {
		_, err := fmt.Fprintln(w, "settings: the repository matches the declaration")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "AREA\tSETTING\tLIVE\tDECLARED"); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Area, c.Setting, c.Live, c.Declared); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func joinSorted(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}

// areas reports which API calls an apply needs for a set of changes.
func areas(changes []Change) map[string]bool {
	found := map[string]bool{}
	for _, c := range changes {
		found[c.Area] = true
	}
	return found
}
