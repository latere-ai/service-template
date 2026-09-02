// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"os"
	"strings"
	"testing"
)

func TestYAMLSubsetShapes(t *testing.T) {
	doc := `---
# a leading comment
name: widget
quoted: "a # b"
single: 'it''s here'
empty:
nested:
  a: 1
  b:
    c: two
flow: [one, two]
none: []
list:
  - first
  - second
sameColumn:
- alpha
- beta
records:
  - path: a
    mode: generated
  - path: b
    mode: seed
`
	root, err := parseYAML("t.yaml", []byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, _ := root.scalar("t.yaml", "name"); got != "widget" {
		t.Errorf("name = %q", got)
	}
	if got, _ := root.scalar("t.yaml", "quoted"); got != "a # b" {
		t.Errorf("a hash inside quotes was treated as a comment: %q", got)
	}
	if got, _ := root.scalar("t.yaml", "single"); got != "it's here" {
		t.Errorf("single = %q", got)
	}
	if got, _ := root.scalar("t.yaml", "empty"); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := root.get("nested").get("b").get("c"); got == nil || got.str != "two" {
		t.Errorf("nested mapping did not parse: %+v", got)
	}
	for key, want := range map[string][]string{
		"flow":       {"one", "two"},
		"none":       {},
		"list":       {"first", "second"},
		"sameColumn": {"alpha", "beta"},
	} {
		got, err := root.strings("t.yaml", key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	records := root.get("records")
	if records == nil || len(records.items) != 2 {
		t.Fatalf("records did not parse: %+v", records)
	}
	if got, _ := records.items[1].scalar("t.yaml", "mode"); got != "seed" {
		t.Errorf("the second record has mode %q", got)
	}
}

func TestYAMLSubsetRejections(t *testing.T) {
	cases := map[string]string{
		"leading indent":      "  a: 1\n",
		"tab indent":          "a:\n\tb: 1\n",
		"duplicate key":       "a: 1\na: 2\n",
		"not a pair":          "just text\n",
		"unterminated flow":   "a: [one, two\n",
		"flow mapping":        "a: {b: 1}\n",
		"empty flow item":     "a: [one, , two]\n",
		"sequence item empty": "a:\n  -\n",
		"unexpected sequence": "- one\n  - two\n",
		"stray deeper indent": "a: 1\n    b: 2\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseYAML("t.yaml", []byte(body)); err == nil {
				t.Fatalf("accepted:\n%s", body)
			}
		})
	}
}

func TestYAMLScalarTypeErrors(t *testing.T) {
	root, err := parseYAML("t.yaml", []byte("list:\n  - a\nflag: maybe\nnum: many\nname: x\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := root.scalar("t.yaml", "list"); err == nil {
		t.Error("a list was read as a scalar")
	}
	if _, err := root.boolean("t.yaml", "flag"); err == nil {
		t.Error("a word was read as a boolean")
	}
	if _, err := root.integer("t.yaml", "num", 0); err == nil {
		t.Error("a word was read as a number")
	}
	if _, err := root.strings("t.yaml", "name"); err == nil {
		t.Error("a scalar was read as a list")
	}
	if got, err := root.integer("t.yaml", "absent", 7); err != nil || got != 7 {
		t.Errorf("integer default = %d, %v", got, err)
	}
	if got, err := root.boolean("t.yaml", "absent"); err != nil || got {
		t.Errorf("boolean default = %v, %v", got, err)
	}
	if n := (*node)(nil).get("a"); n != nil {
		t.Error("get on a nil node returned a value")
	}
}

func TestEmptyDocumentIsAnEmptyMapping(t *testing.T) {
	root, err := parseYAML("t.yaml", []byte("# only a comment\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if root.kind != kindMapping || len(root.keys) != 0 {
		t.Fatalf("an empty document parsed as %+v", root)
	}
}

func TestManifestAndPlanLookup(t *testing.T) {
	src := skeletonFS(t)
	m, err := LoadManifest(src)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if e, ok := m.Lookup("cmd/service/main.go"); !ok || e.Mode != ModeSeed {
		t.Errorf("Lookup returned %+v, %v", e, ok)
	}
	if _, ok := m.Lookup("nothing"); ok {
		t.Error("Lookup found a path the manifest does not declare")
	}
	plan, err := BuildPlan(src, testConfig())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if f, ok := plan.Lookup("cmd/widget/main.go"); !ok || f.Entry.Path != "cmd/service/main.go" {
		t.Errorf("plan Lookup returned %+v, %v", f.Entry, ok)
	}
	if _, ok := plan.Lookup("cmd/service/main.go"); ok {
		t.Error("the plan is keyed by the skeleton path rather than the target path")
	}
}

func TestDeselectingAMergedFileStripsTheRegionAndKeepsConsumerText(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	compose := read(t, dir, "docker-compose.yaml")
	mustContain(t, compose, "POSTGRES_DB: widget", "the rendered managed region")
	write(t, dir, "docker-compose.yaml", compose+"  cache:\n    image: valkey:8\n")

	setFeature(t, dir, FeatureDatabase, false)
	if code, out, errOut := runCLI(t, src, "sync", "-C", dir); code != ExitOK {
		t.Fatalf("sync exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}

	got := read(t, dir, "docker-compose.yaml")
	mustContain(t, got, "cache:", "the consumer service")
	if strings.Contains(got, "POSTGRES_DB") || strings.Contains(got, MarkerStart) {
		t.Errorf("the managed region survived deselection:\n%s", got)
	}
	if code, _, errOut := runCLI(t, src, "check", "-C", dir); code != ExitOK {
		t.Fatalf("check after deselecting a merged file exited %d\n%s", code, errOut)
	}
}

func TestSyncRecordsASeedFileTheRepositoryAlreadyHolds(t *testing.T) {
	src := skeletonFS(t)
	dir := t.TempDir()
	cfg := testConfig()
	write(t, dir, "README.md", "# hand written\n")
	if err := os.WriteFile(dir+"/"+ConfigFile, cfg.Marshal(), 0o644); err != nil {
		t.Fatalf("write the declaration: %v", err)
	}
	loaded, lock, err := load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := Sync(src, dir, loaded, lock); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := read(t, dir, "README.md"); got != "# hand written\n" {
		t.Errorf("sync overwrote a seed file the repository already held: %q", got)
	}
	next, err := LoadLock(dir)
	if err != nil {
		t.Fatalf("load the lock: %v", err)
	}
	if e, ok := next.Entry("README.md"); !ok || e.Digest != Digest([]byte("# hand written\n")) {
		t.Errorf("the lock records %+v for the seed file", e)
	}
}
