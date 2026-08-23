package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Environment variables the workflow identity provider sets when a job is
// granted an identity token. Their absence means the job cannot exchange a
// short-lived credential, which is the state a release must discover before it
// pushes anything.
const (
	EnvOIDCRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	EnvOIDCRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// RolloutPermissions are the verbs the deploy needs, per resource. The deploy
// credential is scoped to exactly these inside one namespace: a cluster-wide
// administrator credential in a release pipeline turns any pipeline compromise
// into a cluster compromise.
var RolloutPermissions = map[string][]string{
	"deployments":          {"get", "list", "watch", "create", "patch", "update"},
	"services":             {"get", "list", "watch", "create", "patch", "update"},
	"serviceaccounts":      {"get", "list", "create", "patch", "update"},
	"poddisruptionbudgets": {"get", "list", "create", "patch", "update"},
	"pods":                 {"get", "list", "watch"},
	"events":               {"list"},
}

// PreflightOptions is one credential check.
type PreflightOptions struct {
	// SecretsPath is the declared secret set.
	SecretsPath string
	// Namespace is the one namespace the deploy credential may act in.
	Namespace string
	// Now is the reference time for expiry checks.
	Now time.Time
	// Getenv reads the run's environment.
	Getenv func(string) string
	// Out receives the report.
	Out io.Writer
}

// Preflight verifies every credential the run will use before the first
// mutating step. A pipeline that discovers a missing deploy credential after
// pushing an image leaves a published artifact with no corresponding release.
func Preflight(ctx context.Context, r Runner, o PreflightOptions) error {
	if o.Getenv == nil {
		return fmt.Errorf("preflight needs an environment reader")
	}
	if o.Namespace == "" {
		return fmt.Errorf("preflight needs the namespace the deploy credential is scoped to")
	}

	decls, err := LoadSecrets(o.SecretsPath)
	if err != nil {
		return err
	}
	problems := CheckSecrets(decls, o.Now, o.Getenv)
	problems = append(problems, checkFederationAvailable(decls, o.Getenv)...)
	problems = append(problems, checkSigning(ctx, r)...)
	problems = append(problems, checkClusterVerbs(ctx, r, o.Namespace)...)
	problems = append(problems, checkNamespaceBoundary(ctx, r)...)

	sort.Strings(problems)
	report(o.Out, decls, problems)
	if len(problems) > 0 {
		return fmt.Errorf("preflight failed with %d problem(s); nothing has been published:\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

// checkFederationAvailable asserts the job can obtain an identity token at
// all. Without the identity permission the exchange fails later, after the
// build.
func checkFederationAvailable(decls []SecretDecl, getenv func(string) string) []string {
	federated := false
	for _, d := range decls {
		if d.Kind == KindFederated {
			federated = true
			break
		}
	}
	if !federated {
		return nil
	}
	var problems []string
	for _, env := range []string{EnvOIDCRequestURL, EnvOIDCRequestToken} {
		if strings.TrimSpace(getenv(env)) == "" {
			problems = append(problems, fmt.Sprintf(
				"%s is not set, so this job cannot obtain a short-lived credential; grant id-token: write to it", env))
		}
	}
	return problems
}

// checkSigning asserts the signing tool is present and runnable.
func checkSigning(ctx context.Context, r Runner) []string {
	if res := r.Run(ctx, Command{Name: "cosign", Args: []string{"version"}}); res.Err != nil {
		return []string{fmt.Sprintf("the signing tool is unavailable: %v", res.Err)}
	}
	return nil
}

// checkClusterVerbs asserts the deploy credential can do what the rollout
// needs, inside its namespace.
func checkClusterVerbs(ctx context.Context, r Runner, namespace string) []string {
	resources := make([]string, 0, len(RolloutPermissions))
	for resource := range RolloutPermissions {
		resources = append(resources, resource)
	}
	sort.Strings(resources)

	var problems []string
	for _, resource := range resources {
		for _, verb := range RolloutPermissions[resource] {
			res := r.Run(ctx, kubectl("auth", "can-i", verb, resource, "-n", namespace))
			if strings.TrimSpace(res.Output) != "yes" {
				problems = append(problems, fmt.Sprintf(
					"the deploy credential cannot %s %s in %s", verb, resource, namespace))
			}
		}
	}
	return problems
}

// checkNamespaceBoundary asserts the deploy credential stops at its namespace.
// A credential that answers yes here is a cluster credential wearing a
// namespace label.
func checkNamespaceBoundary(ctx context.Context, r Runner) []string {
	var problems []string
	for _, probe := range []struct{ verb, resource string }{
		{"get", "pods"},
		{"create", "deployments"},
		{"get", "secrets"},
	} {
		res := r.Run(ctx, kubectl("auth", "can-i", probe.verb, probe.resource, "--all-namespaces"))
		if strings.TrimSpace(res.Output) == "yes" {
			problems = append(problems, fmt.Sprintf(
				"the deploy credential can %s %s in every namespace; scope it to one", probe.verb, probe.resource))
		}
	}
	return problems
}

// report writes what was checked, so a passing preflight is also a record of
// which credentials the run holds.
func report(w io.Writer, decls []SecretDecl, problems []string) {
	if w == nil {
		return
	}
	printf(w, "### Preflight\n\n| Credential | Kind | Purpose |\n| --- | --- | --- |\n")
	for _, d := range decls {
		printf(w, "| %s | %s | %s |\n", d.Name, d.Kind, oneLine(d.Purpose))
	}
	if len(problems) == 0 {
		printf(w, "\nEvery declared credential is present and scoped.\n")
		return
	}
	printf(w, "\n%d problem(s):\n\n", len(problems))
	for _, p := range problems {
		printf(w, "- %s\n", p)
	}
}
