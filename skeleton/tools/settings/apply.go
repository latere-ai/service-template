package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Apply writes the settings that differ and nothing else.
//
// The read comes first so a repeat run issues no request at all. An apply that
// writes unconditionally cannot be run on a schedule: every run would look
// like a change, and a real change would be invisible among them.
func Apply(ctx context.Context, c *Client, d Declaration, live Live, changes []Change, out io.Writer) error {
	if len(changes) == 0 {
		_, err := fmt.Fprintln(out, "settings: nothing to apply, the repository already matches the declaration")
		return err
	}

	touched := areas(changes)

	if touched[AreaRepository] {
		if err := c.Send(ctx, http.MethodPatch, c.repoPath(""), d.Repository); err != nil {
			return err
		}
		// The progress line is advisory. A failed write must not mask a change
		// the API already accepted.
		_, _ = fmt.Fprintln(out, "settings: applied the repository settings")
	}

	if touched[AreaSecurity] {
		if changed(changes, "vulnerability_alerts") {
			method := http.MethodDelete
			if d.Security.VulnerabilityAlerts {
				method = http.MethodPut
			}
			if err := c.Send(ctx, method, c.repoPath("/vulnerability-alerts"), nil); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "settings: applied the vulnerability alert setting")
		}
		if changed(changes, "secret_scanning") || changed(changes, "secret_scanning_push_protection") {
			body := map[string]any{
				"security_and_analysis": map[string]any{
					"secret_scanning":                 map[string]string{"status": d.Security.SecretScanning},
					"secret_scanning_push_protection": map[string]string{"status": d.Security.SecretScanningPushProtection},
				},
			}
			if err := c.Send(ctx, http.MethodPatch, c.repoPath(""), body); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "settings: applied the secret scanning settings")
		}
	}

	if touched[AreaProtection] {
		path := c.repoPath("/branches/" + d.Protection.Branch + "/protection")
		if err := c.Send(ctx, http.MethodPut, path, protectionPayload(d)); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "settings: applied the branch protection rule")
	}

	return nil
}

// protectionPayload is the whole protection rule. The endpoint replaces the
// rule rather than patching it, so every field is stated on every write.
func protectionPayload(d Declaration) map[string]any {
	p := d.Protection
	return map[string]any{
		"required_status_checks": map[string]any{
			"strict":   p.RequiredStatusChecks.Strict,
			"contexts": p.RequiredStatusChecks.Contexts,
		},
		"enforce_admins": p.EnforceAdmins,
		"required_pull_request_reviews": map[string]any{
			"dismiss_stale_reviews":           p.RequiredPullRequestReviews.DismissStaleReviews,
			"require_code_owner_reviews":      p.RequiredPullRequestReviews.RequireCodeOwnerReviews,
			"required_approving_review_count": p.RequiredPullRequestReviews.RequiredApprovingReviewCount,
		},
		"restrictions":                     nil,
		"required_linear_history":          p.RequiredLinearHistory,
		"allow_force_pushes":               p.AllowForcePushes,
		"allow_deletions":                  p.AllowDeletions,
		"required_conversation_resolution": p.RequiredConversationResolution,
	}
}

func changed(changes []Change, setting string) bool {
	for _, c := range changes {
		if c.Setting == setting {
			return true
		}
	}
	return false
}
