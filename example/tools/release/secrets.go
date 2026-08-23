package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SecretsFile is where the declared secret set lives.
const SecretsFile = "deploy/pipeline-secrets.yaml"

// ExpiryWarning is how close a stored credential may come to its recorded
// expiry before the build fails. A credential that expires during a release is
// a release that stops halfway.
const ExpiryWarning = 30 * 24 * time.Hour

// Credential kinds.
const (
	KindFederated = "federated"
	KindStored    = "stored"
)

// SecretDecl is one credential the pipeline uses.
type SecretDecl struct {
	Name    string   `yaml:"name"`
	Kind    string   `yaml:"kind"`
	Purpose string   `yaml:"purpose"`
	Scope   string   `yaml:"scope"`
	UsedBy  []string `yaml:"used_by"`
	// Subject is the trust policy the receiving side must carry. It is
	// declared here so a reviewer can compare what the pipeline claims
	// against what the identity provider allows.
	Subject string `yaml:"subject"`
	// Workflow is the workflow file the trust policy is scoped to.
	Workflow string `yaml:"workflow"`
	// Env is the repository secret a stored credential arrives in.
	Env string `yaml:"env"`
	// Expires is the recorded expiry of a stored credential, as YYYY-MM-DD.
	Expires string `yaml:"expires"`
}

// secretsDoc is the document shape.
type secretsDoc struct {
	Secrets []SecretDecl `yaml:"secrets"`
}

// LoadSecrets reads the declaration.
func LoadSecrets(path string) ([]SecretDecl, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc secretsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Secrets) == 0 {
		return nil, fmt.Errorf("%s declares no secrets; a pipeline that pushes an image and reaches a cluster uses at least one", path)
	}
	return doc.Secrets, nil
}

// CheckSecrets reports every declaration that is incomplete, every trust
// policy that is too broad, and every stored credential that is missing or
// close to its expiry. getenv reads the environment so a preflight run can
// assert the stored values are actually present.
func CheckSecrets(decls []SecretDecl, now time.Time, getenv func(string) string) []string {
	var problems []string
	seen := map[string]bool{}
	for _, d := range decls {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			problems = append(problems, "a declaration has no name")
			continue
		}
		if seen[name] {
			problems = append(problems, fmt.Sprintf("%s: declared twice", name))
		}
		seen[name] = true

		for _, field := range []struct{ label, value string }{
			{"purpose", d.Purpose},
			{"scope", d.Scope},
		} {
			if strings.TrimSpace(field.value) == "" {
				problems = append(problems, fmt.Sprintf("%s: no %s, so a consumer cannot tell what to grant", name, field.label))
			}
		}
		if len(d.UsedBy) == 0 {
			problems = append(problems, fmt.Sprintf("%s: no used_by, so no job owns it", name))
		}

		switch d.Kind {
		case KindFederated:
			problems = append(problems, checkFederated(name, d)...)
		case KindStored:
			problems = append(problems, checkStored(name, d, now, getenv)...)
		default:
			problems = append(problems, fmt.Sprintf("%s: kind is %q, which is neither %s nor %s",
				name, d.Kind, KindFederated, KindStored))
		}
	}
	sort.Strings(problems)
	return problems
}

// checkFederated asserts the trust policy names a repository, a workflow, and
// a ref pattern. A policy scoped to the organization alone lets any repository
// in it assume the identity, which removes the point of the exercise.
func checkFederated(name string, d SecretDecl) []string {
	var problems []string
	subject := strings.TrimSpace(d.Subject)
	switch subject {
	case "":
		problems = append(problems, fmt.Sprintf("%s: federated credential with no subject; the trust policy is unreviewable", name))
	default:
		owner, rest, ok := strings.Cut(strings.TrimPrefix(subject, "repo:"), "/")
		if !strings.HasPrefix(subject, "repo:") || !ok || owner == "" {
			problems = append(problems, fmt.Sprintf("%s: subject %q does not name a repository as repo:OWNER/NAME", name, subject))
			break
		}
		repo, _, _ := strings.Cut(rest, ":")
		if repo == "" || strings.Contains(repo, "*") {
			problems = append(problems, fmt.Sprintf("%s: subject %q is scoped to the organization; any repository in it could assume this identity", name, subject))
		}
		if !strings.Contains(rest, ":ref:") && !strings.Contains(rest, ":environment:") {
			problems = append(problems, fmt.Sprintf("%s: subject %q is not scoped to a ref or an environment", name, subject))
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(d.Workflow), WorkflowDir+"/") {
		problems = append(problems, fmt.Sprintf("%s: workflow %q is not a file under %s/", name, d.Workflow, WorkflowDir))
	}
	if strings.TrimSpace(d.Expires) != "" {
		problems = append(problems, fmt.Sprintf("%s: a federated credential is issued per run and cannot carry an expiry", name))
	}
	return problems
}

// checkStored asserts a fallback credential is narrow, present, and not about
// to expire. A credential with no expiry is not accepted.
func checkStored(name string, d SecretDecl, now time.Time, getenv func(string) string) []string {
	var problems []string
	if strings.TrimSpace(d.Env) == "" {
		problems = append(problems, fmt.Sprintf("%s: stored credential with no env, so no job can read it", name))
	} else if getenv != nil && strings.TrimSpace(getenv(d.Env)) == "" {
		problems = append(problems, fmt.Sprintf("%s: %s is not set in this run", name, d.Env))
	}

	raw := strings.TrimSpace(d.Expires)
	if raw == "" {
		problems = append(problems, fmt.Sprintf("%s: stored credential with no recorded expiry; a credential that never expires is not accepted", name))
		return problems
	}
	expiry, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: expiry %q is not a date in the form YYYY-MM-DD", name, raw))
		return problems
	}
	remaining := expiry.Sub(now)
	switch {
	case remaining <= 0:
		problems = append(problems, fmt.Sprintf("%s: expired on %s", name, raw))
	case remaining <= ExpiryWarning:
		problems = append(problems, fmt.Sprintf("%s: expires on %s, in %d day(s); rotate it before it stops a release",
			name, raw, int(remaining.Hours()/24)))
	}
	return problems
}

// MissingSecrets reports the declared stored credentials that this run cannot
// read, by name and purpose, so a consumer learns about a missing credential
// during setup rather than during a release.
func MissingSecrets(decls []SecretDecl, getenv func(string) string) []string {
	var missing []string
	for _, d := range decls {
		if d.Kind != KindStored || strings.TrimSpace(d.Env) == "" {
			continue
		}
		if strings.TrimSpace(getenv(d.Env)) == "" {
			missing = append(missing, fmt.Sprintf("%s (%s): %s", d.Name, d.Env, d.Purpose))
		}
	}
	sort.Strings(missing)
	return missing
}
