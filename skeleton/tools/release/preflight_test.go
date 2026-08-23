package main

import (
	"context"
	"strings"
	"testing"
)

// runnerFor builds a preflight runner whose namespace-scoped probes answer
// scoped and whose cluster-wide probes answer wide.
func runnerFor(scoped, wide string) *fakeRunner {
	return &fakeRunner{stubs: []stub{
		{match: "cosign version", result: ok("cosign 2.4.0")},
		{match: "kubectl auth can-i get pods --all-namespaces", result: ok(wide)},
		{match: "kubectl auth can-i create deployments --all-namespaces", result: ok(wide)},
		{match: "kubectl auth can-i get secrets --all-namespaces", result: ok(wide)},
		{match: "kubectl auth can-i", result: ok(scoped)},
	}}
}

func preflightOptions(getenv func(string) string) PreflightOptions {
	return PreflightOptions{
		SecretsPath: "../../" + SecretsFile,
		Namespace:   "service-production",
		Now:         now,
		Getenv:      getenv,
	}
}

// identityEnv is a job that was granted an identity token.
func identityEnv() map[string]string {
	return map[string]string{
		EnvOIDCRequestURL:   "https://token.example/",
		EnvOIDCRequestToken: "a-token",
	}
}

func TestPreflightPassesWithEveryCredentialInPlace(t *testing.T) {
	var summary strings.Builder
	o := preflightOptions(envOf(identityEnv()))
	o.Out = &summary
	if err := Preflight(context.Background(), runnerFor("yes", "no"), o); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	mustContain(t, summary.String(), "### Preflight", "the summary")
	mustContain(t, summary.String(), "present and scoped", "the summary")
}

// A pipeline that discovers a missing credential after pushing an image
// leaves a published artifact with no corresponding release.
func TestPreflightFailsBeforeAnythingIsPublished(t *testing.T) {
	cases := map[string]struct {
		env    map[string]string
		runner *fakeRunner
		want   string
	}{
		"no identity token": {
			env:    map[string]string{},
			runner: runnerFor("yes", "no"),
			want:   "id-token: write",
		},
		"signing unavailable": {
			env: identityEnv(),
			runner: &fakeRunner{stubs: []stub{
				{match: "cosign version", result: fails("command not found")},
				{match: "kubectl auth can-i get pods --all-namespaces", result: ok("no")},
				{match: "kubectl auth can-i create deployments --all-namespaces", result: ok("no")},
				{match: "kubectl auth can-i get secrets --all-namespaces", result: ok("no")},
				{match: "kubectl auth can-i", result: ok("yes")},
			}},
			want: "signing tool is unavailable",
		},
		"cluster verbs denied": {
			env:    identityEnv(),
			runner: runnerFor("no", "no"),
			want:   "cannot",
		},
		// A credential that answers yes across the cluster is a cluster
		// credential wearing a namespace label.
		"credential reaches every namespace": {
			env:    identityEnv(),
			runner: runnerFor("yes", "yes"),
			want:   "every namespace",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var summary strings.Builder
			o := preflightOptions(envOf(c.env))
			o.Out = &summary
			err := Preflight(context.Background(), c.runner, o)
			if err == nil {
				t.Fatalf("Preflight passed with %s", name)
			}
			mustContain(t, err.Error(), c.want, "the failure")
			mustContain(t, err.Error(), "nothing has been published", "the failure")
		})
	}
}

func TestPreflightNeedsANamespaceAndAnEnvironment(t *testing.T) {
	o := preflightOptions(envOf(identityEnv()))
	o.Namespace = ""
	if err := Preflight(context.Background(), runnerFor("yes", "no"), o); err == nil {
		t.Error("Preflight ran without a namespace")
	}
	o = preflightOptions(nil)
	if err := Preflight(context.Background(), runnerFor("yes", "no"), o); err == nil {
		t.Error("Preflight ran without an environment")
	}
}

func TestPreflightReportsAMissingDeclaration(t *testing.T) {
	o := preflightOptions(envOf(identityEnv()))
	o.SecretsPath = t.TempDir() + "/absent.yaml"
	if err := Preflight(context.Background(), runnerFor("yes", "no"), o); err == nil {
		t.Fatal("Preflight ran with no declared secret set")
	}
}

// The probe covers every verb the rollout issues, so a grant that is narrower
// than the rollout fails here rather than mid-deploy.
func TestPreflightProbesEveryRolloutVerb(t *testing.T) {
	r := runnerFor("yes", "no")
	o := preflightOptions(envOf(identityEnv()))
	if err := Preflight(context.Background(), r, o); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	for resource, verbs := range RolloutPermissions {
		for _, verb := range verbs {
			want := "kubectl auth can-i " + verb + " " + resource + " -n service-production"
			if !r.called(want) {
				t.Errorf("preflight did not probe %q", want)
			}
		}
	}
}
