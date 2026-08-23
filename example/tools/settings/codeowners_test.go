package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ownership file that ships with this repository must cover the paths the
// declaration requires, or the scaffold ships a gate nobody owns.
func TestShippedCodeOwnersCoversTheRequiredPaths(t *testing.T) {
	declaration, err := LoadDeclaration(filepath.Join("..", "..", ".github", "settings.yml"))
	if err != nil {
		t.Fatalf("load the declaration: %v", err)
	}
	rules, err := LoadCodeOwners(filepath.Join("..", "..", ".github", "CODEOWNERS"))
	if err != nil {
		t.Fatalf("load the ownership file: %v", err)
	}
	if err := VerifyCoverage(rules, declaration.CodeOwners.RequiredPaths); err != nil {
		t.Fatalf("the shipped ownership file leaves a required path unowned: %v", err)
	}
	if len(declaration.CodeOwners.RequiredPaths) == 0 {
		t.Fatal("the declaration requires no owned path, so owner review guards nothing")
	}
}

// Coverage is about a path having an owner, not about who the owner is.
func TestCodeOwnersCoverage(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "a directory rule covers the files below it",
			content: "* @team\n/.github/ @maintainers\n/.template.yaml @maintainers\n",
		},
		{
			name:    "a missing workflow owner is reported",
			content: "* @team\n/.template.yaml @maintainers\n",
			wantErr: "/.github/",
		},
		{
			name:    "a rule with no owner covers nothing",
			content: "/.github/\n/.template.yaml @maintainers\n",
			wantErr: "/.github/",
		},
		{
			name:    "comments and blank lines are ignored",
			content: "# ownership\n\n/.github/ @maintainers\n",
			wantErr: "/.template.yaml",
		},
	}

	required := []string{"/.github/", "/.template.yaml"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CODEOWNERS")
			if err := os.WriteFile(path, []byte(c.content), 0o644); err != nil {
				t.Fatalf("write the ownership file: %v", err)
			}
			rules, err := LoadCodeOwners(path)
			if err != nil {
				t.Fatalf("load the ownership file: %v", err)
			}
			err = VerifyCoverage(rules, required)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("coverage failed: %v", err)
			case c.wantErr != "" && err == nil:
				t.Fatalf("coverage passed with %s unowned", c.wantErr)
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Fatalf("the failure does not name %s: %v", c.wantErr, err)
			}
		})
	}
}

// The verify mode runs without credentials, so a pull request can run it, and
// it reports a placeholder owner without failing a fresh scaffold.
func TestVerifyMode(t *testing.T) {
	dir := t.TempDir()
	owners := filepath.Join(dir, "CODEOWNERS")
	if err := os.WriteFile(owners, []byte("* @OWNER\n/.github/ @OWNER\n/.template.yaml @OWNER\n"), 0o644); err != nil {
		t.Fatalf("write the ownership file: %v", err)
	}
	settings := writeTemp(t, "settings.yml", declaredSettings(t))

	got := runSettings(t, "-mode", "verify", "-file", settings, "-codeowners", owners)
	if got.Code != exitOK {
		t.Fatalf("verify exited %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	}
	if !strings.Contains(got.Stdout, "::warning file=") ||
		!strings.Contains(got.Stdout, "placeholder owner") {
		t.Fatalf("verify did not raise an annotation for the placeholder owner\n%s", got.Stdout)
	}

	if err := os.WriteFile(owners, []byte("* @team\n"), 0o644); err != nil {
		t.Fatalf("write the ownership file: %v", err)
	}
	got = runSettings(t, "-mode", "verify", "-file", settings, "-codeowners", owners)
	if got.Code == exitOK {
		t.Fatalf("verify passed with the workflow directory unowned\n%s", got.Stdout)
	}
}
