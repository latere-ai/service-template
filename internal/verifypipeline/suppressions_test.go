package verifypipeline

import (
	"path/filepath"
	"testing"
)

const liveEntry = `suppressions:
  - id: GO-2030-0001
    tool: govulncheck
    reason: the advisory is on a path this service does not call, tracked in the update queue
    expires: 2099-01-01
`

// A suppression carries an expiry so that a silence is revisited. An entry
// past its expiry fails the build, which is the only way an old decision gets
// looked at again.
func TestSuppressionExpiry(t *testing.T) {
	cases := []struct {
		name    string
		content string
		code    int
		want    string
	}{
		{"a live entry passes", liveEntry, 0, "every entry is complete and unexpired"},
		{
			name: "an expired entry fails",
			content: `suppressions:
  - id: GO-2030-0001
    tool: govulncheck
    reason: the advisory is on a path this service does not call
    expires: 2020-01-01
`,
			code: 1,
			want: "past its expiry 2020-01-01",
		},
		{
			name: "an entry with no reason fails",
			content: `suppressions:
  - id: GO-2030-0001
    tool: govulncheck
    expires: 2099-01-01
`,
			code: 1,
			want: "without id or path, tool, reason, and expires",
		},
		{
			name: "an entry with a malformed expiry fails",
			content: `suppressions:
  - id: GO-2030-0001
    tool: govulncheck
    reason: the advisory is on a path this service does not call
    expires: next year
`,
			code: 1,
			want: "which is not YYYY-MM-DD",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, ".github", "suppressions.yml")
			writeFile(t, file, c.content)
			got := runScript(t, dir, "suppressions.sh", nil, "--file", file, "--root", dir)
			if got.Code != c.code {
				t.Fatalf("exit code %d, want %d\n%s", got.Code, c.code, got.Output)
			}
			got.contains(t, c.want)
		})
	}
}

// An inline comment with no tracked entry is the shape that accumulates: it
// silences a finding where nobody reads it and it never expires.
func TestInlineSuppressionNeedsAnEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".github", "suppressions.yml")
	writeFile(t, file, liveEntry)

	writeFile(t, filepath.Join(dir, "internal", "a", "a.go"),
		"package a\n\nvar x = 1 //nolint:gochecknoglobals // suppression:GO-2030-0001\n")
	got := runScript(t, dir, "suppressions.sh", nil, "--file", file, "--root", dir)
	if got.Code != 0 {
		t.Fatalf("a marker with a live entry failed\n%s", got.Output)
	}

	writeFile(t, filepath.Join(dir, "internal", "a", "b.go"),
		"package a\n\nvar y = 2 //nolint:all\n")
	got = runScript(t, dir, "suppressions.sh", nil, "--file", file, "--root", dir)
	if got.Code == 0 {
		t.Fatalf("an inline suppression with no entry passed\n%s", got.Output)
	}
	got.contains(t, "internal/a/b.go")
	got.contains(t, "no live entry covers it")
}

// A marker naming an entry that does not exist, or one that has expired, is
// the same failure as no entry at all.
func TestInlineSuppressionNamingAnUnknownEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".github", "suppressions.yml")
	writeFile(t, file, liveEntry)
	writeFile(t, filepath.Join(dir, "internal", "a", "a.go"),
		"package a\n\nvar x = 1 //nolint:all // suppression:GO-2030-9999\n")
	got := runScript(t, dir, "suppressions.sh", nil, "--file", file, "--root", dir)
	if got.Code == 0 {
		t.Fatalf("a marker naming an undeclared entry passed\n%s", got.Output)
	}
	got.contains(t, "names GO-2030-9999")
}

// A linter directive carries no advisory identifier, so an entry may name the
// file it lives in instead. The entry still expires, which is what keeps it
// from becoming the permanent silence an inline comment alone would be.
func TestPathEntryCoversAFileWithoutAnIdentifier(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".github", "suppressions.yml")
	writeFile(t, filepath.Join(dir, "internal", "a", "a.go"),
		"package a\n\nvar x = 1 //nolint:gochecknoglobals\n")

	writeFile(t, file, `suppressions:
  - path: internal/a/a.go
    tool: golangci-lint
    reason: the directive is reviewed with the file it sits in
    expires: 2099-01-01
`)
	got := runScript(t, dir, "suppressions.sh", nil, "--file", file, "--root", dir)
	if got.Code != 0 {
		t.Fatalf("a path entry did not cover the file\n%s", got.Output)
	}

	writeFile(t, file, `suppressions:
  - path: internal/a/a.go
    tool: golangci-lint
    reason: the directive is reviewed with the file it sits in
    expires: 2020-01-01
`)
	got = runScript(t, dir, "suppressions.sh", nil, "--file", file, "--root", dir)
	if got.Code == 0 {
		t.Fatalf("an expired path entry still covered the file\n%s", got.Output)
	}
	got.contains(t, "past its expiry")
}

// A repository with no suppression file suppresses nothing, which is the
// state every new repository starts in.
func TestNoSuppressionFile(t *testing.T) {
	dir := t.TempDir()
	got := runScript(t, dir, "suppressions.sh", nil,
		"--file", filepath.Join(dir, ".github", "suppressions.yml"), "--root", dir)
	if got.Code != 0 {
		t.Fatalf("an empty repository failed the suppression rules\n%s", got.Output)
	}
	got.contains(t, "nothing is suppressed")
}

// The identifier list is how a scanner report asks what it may ignore, so it
// must exclude an expired entry.
func TestSuppressionIdentifierList(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "suppressions.yml")
	writeFile(t, file, `suppressions:
  - id: GO-2030-0001
    tool: govulncheck
    reason: unreached path
    expires: 2099-01-01
  - id: GO-2030-0002
    tool: govulncheck
    reason: unreached path
    expires: 2020-01-01
  - id: JS-0003
    tool: eslint
    reason: rule replaced upstream
    expires: 2099-01-01
`)
	got := runScript(t, dir, "suppressions.sh", nil, "--file", file, "--ids", "govulncheck")
	if got.Code != 0 {
		t.Fatalf("listing identifiers failed\n%s", got.Output)
	}
	if got.Output != "GO-2030-0001\n" {
		t.Fatalf("the identifier list is %q, want only the live govulncheck entry", got.Output)
	}
}
