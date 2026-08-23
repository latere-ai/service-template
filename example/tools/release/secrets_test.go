package main

import (
	"strings"
	"testing"
	"time"
)

// now is the reference time every expiry case is measured from.
var now = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func federated() SecretDecl {
	return SecretDecl{
		Name: "registry", Kind: KindFederated,
		Purpose: "Push the release image.", Scope: "This repository's packages.",
		UsedBy: []string{"build"}, Subject: "repo:owner/name:ref:refs/tags/v*",
		Workflow: DefaultReleaseWorkflow,
	}
}

func stored(expires string) SecretDecl {
	return SecretDecl{
		Name: "legacy", Kind: KindStored,
		Purpose: "Reach a service with no federation support.", Scope: "One project.",
		UsedBy: []string{"deploy"}, Env: "LEGACY_TOKEN", Expires: expires,
	}
}

func TestShippedSecretDeclarationIsComplete(t *testing.T) {
	decls, err := LoadSecrets("../../" + SecretsFile)
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	problems := CheckSecrets(decls, now, func(string) string { return "" })
	if len(problems) > 0 {
		t.Fatalf("the shipped declaration has problems:\n  %s", strings.Join(problems, "\n  "))
	}
	for _, d := range decls {
		if d.Kind != KindFederated {
			t.Errorf("%s is %s; the pipeline's own credentials are all federated", d.Name, d.Kind)
		}
	}
}

func TestCheckSecretsAcceptsACompleteDeclaration(t *testing.T) {
	env := envOf(map[string]string{"LEGACY_TOKEN": "value"})
	problems := CheckSecrets([]SecretDecl{federated(), stored("2027-01-01")}, now, env)
	if len(problems) > 0 {
		t.Fatalf("problems = %v", problems)
	}
}

// A policy scoped to the organization alone lets any repository in it assume
// the identity.
func TestCheckSecretsRejectsABroadTrustPolicy(t *testing.T) {
	cases := map[string]string{
		"organization wide": "repo:owner/*:ref:refs/tags/v*",
		"no ref":            "repo:owner/name",
		"not a repository":  "owner/name",
		"empty":             "",
	}
	for name, subject := range cases {
		t.Run(name, func(t *testing.T) {
			d := federated()
			d.Subject = subject
			if problems := CheckSecrets([]SecretDecl{d}, now, nil); len(problems) == 0 {
				t.Fatalf("CheckSecrets accepted the subject %q", subject)
			}
		})
	}
}

func TestCheckSecretsRequiresAWorkflowScope(t *testing.T) {
	d := federated()
	d.Workflow = "release.yml"
	if problems := CheckSecrets([]SecretDecl{d}, now, nil); !problemsContain(problems, "not a file under") {
		t.Fatalf("problems = %v", problems)
	}
}

// A credential that expires during a release is a release that stops halfway.
func TestStoredCredentialNearItsExpiryFailsTheBuild(t *testing.T) {
	env := envOf(map[string]string{"LEGACY_TOKEN": "value"})
	cases := map[string]struct {
		expires string
		fails   bool
	}{
		"far away":       {"2027-01-01", false},
		"just outside":   {"2026-07-05", false},
		"inside thirty":  {"2026-06-20", true},
		"already passed": {"2026-05-01", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			problems := CheckSecrets([]SecretDecl{stored(c.expires)}, now, env)
			if c.fails != (len(problems) > 0) {
				t.Fatalf("problems = %v, want a failure: %v", problems, c.fails)
			}
		})
	}
}

func TestStoredCredentialWithoutAnExpiryIsRejected(t *testing.T) {
	env := envOf(map[string]string{"LEGACY_TOKEN": "value"})
	if problems := CheckSecrets([]SecretDecl{stored("")}, now, env); !problemsContain(problems, "no recorded expiry") {
		t.Fatalf("problems = %v", problems)
	}
	if problems := CheckSecrets([]SecretDecl{stored("soon")}, now, env); !problemsContain(problems, "YYYY-MM-DD") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestCheckSecretsReportsAMissingStoredValue(t *testing.T) {
	problems := CheckSecrets([]SecretDecl{stored("2027-01-01")}, now, func(string) string { return "" })
	if !problemsContain(problems, "LEGACY_TOKEN is not set") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestCheckSecretsRejectsIncompleteDeclarations(t *testing.T) {
	cases := map[string]SecretDecl{
		"no name":      {Kind: KindFederated},
		"no purpose":   {Name: "a", Kind: KindFederated, Scope: "s", UsedBy: []string{"build"}, Subject: "repo:o/n:ref:refs/tags/v*", Workflow: DefaultReleaseWorkflow},
		"no scope":     {Name: "a", Kind: KindFederated, Purpose: "p", UsedBy: []string{"build"}, Subject: "repo:o/n:ref:refs/tags/v*", Workflow: DefaultReleaseWorkflow},
		"no user":      {Name: "a", Kind: KindFederated, Purpose: "p", Scope: "s", Subject: "repo:o/n:ref:refs/tags/v*", Workflow: DefaultReleaseWorkflow},
		"unknown kind": {Name: "a", Kind: "shared", Purpose: "p", Scope: "s", UsedBy: []string{"build"}},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if problems := CheckSecrets([]SecretDecl{d}, now, nil); len(problems) == 0 {
				t.Fatalf("CheckSecrets accepted %+v", d)
			}
		})
	}
}

func TestCheckSecretsRejectsADuplicateAndAFederatedExpiry(t *testing.T) {
	if problems := CheckSecrets([]SecretDecl{federated(), federated()}, now, nil); !problemsContain(problems, "declared twice") {
		t.Fatalf("problems = %v", problems)
	}
	d := federated()
	d.Expires = "2027-01-01"
	if problems := CheckSecrets([]SecretDecl{d}, now, nil); !problemsContain(problems, "cannot carry an expiry") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestMissingSecretsNamesEachOneAndItsPurpose(t *testing.T) {
	missing := MissingSecrets([]SecretDecl{federated(), stored("2027-01-01")}, func(string) string { return "" })
	if len(missing) != 1 {
		t.Fatalf("missing = %v", missing)
	}
	mustContain(t, missing[0], "LEGACY_TOKEN", "the report")
	mustContain(t, missing[0], "no federation support", "the report")
}

func TestLoadSecretsRejectsAnEmptyOrMalformedFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadSecrets(writeFile(t, dir, "empty.yaml", "secrets: []\n")); err == nil {
		t.Error("LoadSecrets accepted a declaration with no secrets")
	}
	if _, err := LoadSecrets(writeFile(t, dir, "bad.yaml", "secrets: [\n")); err == nil {
		t.Error("LoadSecrets accepted unparsable YAML")
	}
	if _, err := LoadSecrets(dir + "/absent.yaml"); err == nil {
		t.Error("LoadSecrets accepted a file that does not exist")
	}
}
