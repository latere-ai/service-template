package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestLoadManifestFailsOnADuplicatePath(t *testing.T) {
	_, err := LoadManifest(os.DirFS("testdata/duplicate-skeleton"))
	if err == nil {
		t.Fatal("two fragments declaring the same path were accepted")
	}
	mustContain(t, err.Error(), "duplicate path", "the duplicate report")
	mustContain(t, err.Error(), "manifests/core.yaml", "the fragment that declared it first")
}

func TestLoadManifestRejectsBadDeclarations(t *testing.T) {
	cases := []struct {
		name     string
		fragment string
		want     string
	}{
		{"no mode", "files:\n  - path: a.txt\n", "needs a mode"},
		{"unknown mode", "files:\n  - path: a.txt\n    mode: copied\n", "the modes are generated, seed, merged"},
		{"no path", "files:\n  - mode: generated\n", "needs a path"},
		{"absolute path", "files:\n  - path: /etc/passwd\n    mode: generated\n", "must be relative"},
		{"escaping path", "files:\n  - path: ../outside\n    mode: generated\n", "must be relative"},
		{"template suffix", "files:\n  - path: a.txt.tmpl\n    mode: generated\n", "without the .tmpl suffix"},
		{"declares the lock", "files:\n  - path: template.lock\n    mode: generated\n", "written by the generator"},
		{"unknown profile", "files:\n  - path: a.txt\n    mode: generated\n    profiles: [gateway]\n", "unknown profile"},
		{"unknown feature", "files:\n  - path: a.txt\n    mode: generated\n    features: [queue]\n", "unknown feature flag"},
		{"unknown field", "files:\n  - path: a.txt\n    mode: generated\n    owner: nobody\n", "unknown file field"},
		{"not a list", "files: a.txt\n", "must be a list"},
		{"no files key", "other: 1\n", "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := fstest.MapFS{
				"manifests/core.yaml": {Data: []byte(c.fragment)},
				"a.txt":               {Data: []byte("a\n")},
			}
			_, err := LoadManifest(src)
			if err == nil {
				t.Fatalf("accepted %q", c.fragment)
			}
			mustContain(t, err.Error(), c.want, "the failure reason")
		})
	}
}

func TestLoadManifestNeedsAFragment(t *testing.T) {
	if _, err := LoadManifest(fstest.MapFS{}); err == nil {
		t.Fatal("a skeleton with no fragments was accepted")
	}
}

func TestVerifyCoverageFindsUndeclaredAndMissingFiles(t *testing.T) {
	if err := VerifyCoverage(skeletonFS(t)); err != nil {
		t.Fatalf("the fixture skeleton is not fully declared: %v", err)
	}

	extra := fstest.MapFS{
		"manifests/core.yaml": {Data: []byte("files:\n  - path: a.txt\n    mode: generated\n  - path: gone.txt\n    mode: seed\n")},
		"a.txt":               {Data: []byte("a\n")},
		"stray.txt":           {Data: []byte("stray\n")},
		"out/binary":          {Data: []byte("build output\n")},
	}
	err := VerifyCoverage(extra)
	if err == nil {
		t.Fatal("an undeclared file passed the coverage check")
	}
	mustContain(t, err.Error(), "stray.txt", "the undeclared file")
	mustContain(t, err.Error(), "gone.txt", "the declared path with no file")
	if strings.Contains(err.Error(), "out/binary") {
		t.Errorf("build output was reported as template content:\n%s", err)
	}
}

// The frontend build copies its bundle into the directory the binary embeds,
// which also holds one committed placeholder. A maintainer who runs that copy
// must not turn every emitted asset into an undeclared skeleton file.
func TestVerifyCoverageIgnoresTheEmbeddedBundle(t *testing.T) {
	const dir = "internal/web/public"
	fixture := fstest.MapFS{
		"manifests/web.yaml": {Data: []byte(
			"files:\n  - path: " + dir + "/index.html\n    mode: seed\n")},
		dir + "/index.html":            {Data: []byte("<html></html>\n")},
		dir + "/assets/index-a1b2.js":  {Data: []byte("console.log(1)\n")},
		dir + "/assets/index-a1b2.css": {Data: []byte("body{}\n")},
	}
	if err := VerifyCoverage(fixture); err != nil {
		t.Fatalf("a built bundle was reported as undeclared: %v", err)
	}

	// The declared placeholder is still required, so the directory is not a
	// hole the coverage check stops looking into.
	missing := fstest.MapFS{
		"manifests/web.yaml": {Data: []byte(
			"files:\n  - path: " + dir + "/index.html\n    mode: seed\n")},
		dir + "/assets/index-a1b2.js": {Data: []byte("console.log(1)\n")},
	}
	err := VerifyCoverage(missing)
	if err == nil {
		t.Fatal("a missing placeholder passed the coverage check")
	}
	mustContain(t, err.Error(), dir+"/index.html", "the declared path with no file")
}

// The lint configuration is rendered before every lint run and gitignored, so
// it is present in any working tree where `make lint` has run and is declared
// by no fragment. Reporting it would mean linting the skeleton breaks the
// manifest check.
func TestVerifyCoverageIgnoresTheRenderedLintConfig(t *testing.T) {
	fixture := fstest.MapFS{
		"manifests/core.yaml": {Data: []byte("files:\n  - path: a.txt\n    mode: generated\n")},
		"a.txt":               {Data: []byte("a\n")},
		".golangci.yml":       {Data: []byte("version: \"2\"\n")},
	}
	if err := VerifyCoverage(fixture); err != nil {
		t.Fatalf("the rendered lint configuration was reported as undeclared: %v", err)
	}
}

func TestSplitRegionRejectsBrokenMarkers(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no markers", "all: build\n", "carries no managed region"},
		{"start only", MarkerStart + "\nX := 1\n", "start marker with no end marker"},
		{"end only", "X := 1\n" + MarkerEnd + "\n", "end marker with no start marker"},
		{"reversed", MarkerEnd + "\nX := 1\n" + MarkerStart + "\n", "above the start marker"},
		{"two regions", MarkerStart + "\nA\n" + MarkerEnd + "\n" + MarkerStart + "\nB\n" + MarkerEnd + "\n",
			"the template owns exactly one region"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := SplitRegion("Makefile", []byte(c.body))
			if err == nil {
				t.Fatalf("accepted %q", c.body)
			}
			mustContain(t, err.Error(), c.want, "the marker failure")
		})
	}
}

func TestSpliceAndStripPreserveConsumerText(t *testing.T) {
	disk := []byte("head\n" + MarkerStart + "\nold\n" + MarkerEnd + "\ntail\n")
	spliced, err := Splice("Makefile", disk, []string{"new", "lines"})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	want := "head\n" + MarkerStart + "\nnew\nlines\n" + MarkerEnd + "\ntail\n"
	if string(spliced) != want {
		t.Fatalf("splice produced:\n%q\nwant:\n%q", spliced, want)
	}
	stripped, err := StripRegion("Makefile", spliced)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if string(stripped) != "head\ntail\n" {
		t.Fatalf("strip produced %q", stripped)
	}
}

func TestMarkersTolerateCarriageReturns(t *testing.T) {
	disk := []byte("head\r\n" + MarkerStart + "\r\nold\r\n" + MarkerEnd + "\r\ntail\r\n")
	if _, err := SplitRegion("Makefile", disk); err != nil {
		t.Fatalf("a CRLF checkout failed to parse: %v", err)
	}
}

func TestTargetPathRenamesOnlyTheCommandDirectory(t *testing.T) {
	cases := map[string]string{
		"cmd/service":            "cmd/widget",
		"cmd/service/main.go":    "cmd/widget/main.go",
		"cmd/servicebus/main.go": "cmd/servicebus/main.go",
		"internal/service.go":    "internal/service.go",
		".lateregate.yaml":       ".lateregate.yaml",
	}
	for in, want := range cases {
		if got := TargetPath(in, "widget"); got != want {
			t.Errorf("TargetPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderFailsOnAnUnknownTemplateField(t *testing.T) {
	src := fstest.MapFS{
		"manifests/core.yaml": {Data: []byte("files:\n  - path: a.txt\n    mode: generated\n")},
		"a.txt.tmpl":          {Data: []byte("{{ .Nope }}\n")},
	}
	cfg := testConfig()
	if _, err := BuildPlan(src, cfg); err == nil {
		t.Fatal("a template field that does not exist rendered successfully")
	}
}

func TestRenderLeavesBinaryContentAlone(t *testing.T) {
	binary := []byte("\x00\x01example.com/service\x00")
	src := fstest.MapFS{
		"manifests/core.yaml": {Data: []byte("files:\n  - path: logo.bin\n    mode: generated\n")},
		"logo.bin":            {Data: binary},
	}
	plan, err := BuildPlan(src, testConfig())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if string(plan.Files[0].Content) != string(binary) {
		t.Fatalf("binary content was rewritten: %q", plan.Files[0].Content)
	}
}

func TestBuildPlanRejectsAMergedFileWithoutMarkers(t *testing.T) {
	src := fstest.MapFS{
		"manifests/core.yaml": {Data: []byte("files:\n  - path: Makefile\n    mode: merged\n")},
		"Makefile":            {Data: []byte("all:\n\tgo build ./...\n")},
	}
	_, err := BuildPlan(src, testConfig())
	if err == nil {
		t.Fatal("a merged skeleton file with no managed region was accepted")
	}
	mustContain(t, err.Error(), "carries no managed region", "the marker failure")
}

func TestLockRoundTrip(t *testing.T) {
	l := &Lock{
		Version:  "v1.4.0",
		Profile:  ProfileService,
		Features: map[string]bool{FeatureFrontend: true},
		Files: []LockEntry{
			{Path: "b.txt", Mode: ModeGenerated, Digest: Digest([]byte("b"))},
			{Path: "a.txt", Mode: ModeMerged, Digest: Digest([]byte("a"))},
		},
	}
	data := l.Marshal()
	back, err := ParseLock(LockFile, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(back.Marshal()) != string(data) {
		t.Fatalf("the lock did not round trip:\n%s\n%s", data, back.Marshal())
	}
	if back.Files[0].Path != "a.txt" {
		t.Errorf("the lock is not sorted by path: %+v", back.Files)
	}
	if _, ok := back.Entry("b.txt"); !ok {
		t.Error("Entry did not find a recorded path")
	}
}

func TestParseLockRejectsBadDocuments(t *testing.T) {
	cases := map[string]string{
		"unknown field":  "colour: red\n",
		"duplicate path": "files:\n  - path: a\n    mode: seed\n    digest: x\n  - path: a\n    mode: seed\n    digest: y\n",
		"unknown mode":   "files:\n  - path: a\n    mode: copied\n    digest: x\n",
		"files not list": "files: a\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseLock(LockFile, []byte(body)); err == nil {
				t.Fatalf("accepted %q", body)
			}
		})
	}
}

func TestLoadLockWithoutAFileIsEmpty(t *testing.T) {
	l, err := LoadLock(t.TempDir())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(l.Files) != 0 {
		t.Fatalf("an absent lock produced %d entries", len(l.Files))
	}
}

func TestParseConfigRejectsBadDeclarations(t *testing.T) {
	base := "template: t\nversion: v1.0.0\nmodule: github.com/acme/widget\nname: widget\nprofile: service\n"
	cases := map[string]string{
		"bad version":     strings.Replace(base, "version: v1.0.0", "version: 1.0", 1),
		"missing module":  strings.Replace(base, "module: github.com/acme/widget\n", "", 1),
		"bad name":        strings.Replace(base, "name: widget", "name: Widget_1", 1),
		"unknown profile": strings.Replace(base, "profile: service", "profile: gateway", 1),
		"unknown field":   base + "colour: red\n",
		"unknown flag":    base + "features:\n  queue: true\n",
		// The coverage gate is configured in .lateregate.yaml now. A
		// declaration still carrying the old block is rejected rather than
		// ignored, so a repository cannot keep setting a threshold that
		// nothing reads.
		"coverage moved to .lateregate.yaml": base + "coverage:\n  threshold: 90\n",
		"seo without frontend":               base + "features:\n  seo: true\n",
		"waiver without reason":              base + "waivers:\n  - path: a\n    expires: 2026-01-01\n",
		"waiver without expiry":              base + "waivers:\n  - path: a\n    reason: because\n",
		"waiver bad expiry":                  base + "waivers:\n  - path: a\n    reason: because\n    expires: soon\n",
		"tab indentation":                    base + "features:\n\tseo: true\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(ConfigFile, []byte(body)); err == nil {
				t.Fatalf("accepted:\n%s", body)
			}
		})
	}
}

func TestFrontendOnlyProfileAlwaysBuildsAFrontend(t *testing.T) {
	body := "template: t\nversion: v1.0.0\nmodule: github.com/acme/site\nname: site\nprofile: frontend-only\n"
	cfg, err := ParseConfig(ConfigFile, []byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.Enabled(FeatureFrontend) {
		t.Error("the frontend-only profile did not enable the frontend flag")
	}
	off := body + "features:\n  frontend: false\n"
	if _, err := ParseConfig(ConfigFile, []byte(off)); err == nil {
		t.Error("the frontend-only profile accepted frontend: false")
	}
}

func TestConfigMarshalRoundTrips(t *testing.T) {
	cfg := testConfig()
	cfg.Waivers = []Waiver{{Path: "a", Reason: "because", Expires: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}}
	back, err := ParseConfig(ConfigFile, cfg.Marshal())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(back.Marshal()) != string(cfg.Marshal()) {
		t.Fatalf("the declaration did not round trip:\n%s\n%s", cfg.Marshal(), back.Marshal())
	}
	if w, ok := back.WaiverFor("a"); !ok || w.Reason != "because" {
		t.Errorf("WaiverFor returned %+v, %v", w, ok)
	}
}

func TestSetVersionLineNeedsExactlyOneVersion(t *testing.T) {
	if _, err := SetVersionLine([]byte("module: a\n"), "v1.0.0"); err == nil {
		t.Error("a declaration with no version field was accepted")
	}
	if _, err := SetVersionLine([]byte("version: v1.0.0\nversion: v1.0.1\n"), "v1.0.0"); err == nil {
		t.Error("a declaration with two version fields was accepted")
	}
	out, err := SetVersionLine([]byte("version: v1.0.0 # pinned\nmodule: a\n"), "v1.1.0")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if string(out) != "version: v1.1.0 # pinned\nmodule: a\n" {
		t.Fatalf("got %q", out)
	}
}

func TestUnifiedDiffShowsBothSides(t *testing.T) {
	got := UnifiedDiff("a.txt", []byte("one\ntwo\nthree\n"), []byte("one\nTWO\nthree\n"))
	mustContain(t, got, "--- template/a.txt", "the template side")
	mustContain(t, got, "+++ repository/a.txt", "the repository side")
	mustContain(t, got, "-two", "the removed line")
	mustContain(t, got, "+TWO", "the added line")
	if UnifiedDiff("a.txt", []byte("same\n"), []byte("same\n")) != "" {
		t.Error("identical content produced a diff")
	}
}

func TestUnifiedDiffSummarizesLargeFiles(t *testing.T) {
	a := strings.Repeat("line\n", diffLineBudget)
	b := strings.Repeat("other\n", diffLineBudget)
	got := UnifiedDiff("big.txt", []byte(a), []byte(b))
	mustContain(t, got, "template lines against", "the bounded summary")
}

func TestCommandUsageAndUnknownCommand(t *testing.T) {
	src := skeletonFS(t)
	if code, out, _ := runCLI(t, src, "help"); code != ExitOK || !strings.Contains(out, "template init") {
		t.Errorf("help exited %d with:\n%s", code, out)
	}
	if code, _, errOut := runCLI(t, src); code != ExitError || !strings.Contains(errOut, "Usage") {
		t.Errorf("no arguments exited %d with:\n%s", code, errOut)
	}
	if code, _, errOut := runCLI(t, src, "frobnicate"); code != ExitError ||
		!strings.Contains(errOut, "unknown command") {
		t.Errorf("an unknown command exited %d with:\n%s", code, errOut)
	}
}

func TestInitCommandNeedsAModule(t *testing.T) {
	code, _, errOut := runCLI(t, skeletonFS(t), "init", "-C", t.TempDir())
	if code != ExitError {
		t.Fatalf("init without a module exited %d", code)
	}
	mustContain(t, errOut, "needs -module", "the missing flag")
}

func TestInitCommandDefaultsTheNameToTheModuleBase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	code, _, errOut := runCLI(t, skeletonFS(t), "init", "-C", dir,
		"-module", "github.com/acme/widget", "-features", "frontend, database")
	if code != ExitOK {
		t.Fatalf("init exited %d\n%s", code, errOut)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "widget" {
		t.Errorf("the name defaulted to %q, want widget", cfg.Name)
	}
	if !cfg.Enabled(FeatureFrontend) || !cfg.Enabled(FeatureDatabase) {
		t.Errorf("the feature list was not parsed: %+v", cfg.Features)
	}
	if cfg.Version != testVersion {
		t.Errorf("init recorded version %q, want %q", cfg.Version, testVersion)
	}
}

func TestManifestCommandValidatesTheSkeleton(t *testing.T) {
	if code, out, errOut := runCLI(t, skeletonFS(t), "manifest"); code != ExitOK {
		t.Fatalf("manifest exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if code, _, _ := runCLI(t, os.DirFS("testdata/duplicate-skeleton"), "manifest"); code != ExitError {
		t.Fatalf("manifest accepted a duplicate declaration, exit %d", code)
	}
}

func TestCommandsFailOnAMissingDeclaration(t *testing.T) {
	src := skeletonFS(t)
	empty := t.TempDir()
	for _, command := range []string{"sync", "check", "upgrade"} {
		if code, _, _ := runCLI(t, src, command, "-C", empty); code != ExitError {
			t.Errorf("%s in a repository with no declaration exited %d", command, code)
		}
	}
}

func TestUpgradeNeedsAVersion(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	var out, errOut = &strings.Builder{}, &strings.Builder{}
	code := Run(Env{Skeleton: src, Stdout: out, Stderr: errOut, Now: testNow}, []string{"upgrade", "-C", dir})
	if code != ExitError {
		t.Fatalf("upgrade without a version exited %d", code)
	}
	mustContain(t, errOut.String(), "needs -version", "the missing flag")
}
