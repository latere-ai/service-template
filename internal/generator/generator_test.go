package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesTheSelectedFiles(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())

	for _, want := range []string{
		ConfigFile, LockFile,
		".golangci.yml", ".githooks/pre-commit", "Makefile", "README.md",
		"cmd/widget/main.go", "internal/version/version.go",
		"deploy/service.yaml", "internal/store/store.go", "frontend/index.html",
	} {
		if !exists(t, dir, want) {
			t.Errorf("init did not write %s", want)
		}
	}
	for _, unwanted := range []string{"lib/doc.go", "cmd/service/main.go"} {
		if exists(t, dir, unwanted) {
			t.Errorf("init wrote %s, which the service profile does not select", unwanted)
		}
	}
}

func TestInitRewritesModuleAndCommandDirectory(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())

	main := read(t, dir, "cmd/widget/main.go")
	mustContain(t, main, `"github.com/acme/widget/internal/version"`, "module substitution")
	mustContain(t, main, `"widget"`, "name substitution in a template")

	version := read(t, dir, "internal/version/version.go")
	mustContain(t, version, `"github.com/acme/widget"`, "module substitution in a plain file")
	if strings.Contains(version, SkeletonModule) {
		t.Errorf("the skeleton module path survived generation:\n%s", version)
	}

	makefile := read(t, dir, "Makefile")
	mustContain(t, makefile, "./cmd/widget", "cmd directory rewrite in file content")

	// The rewrite is anchored on two literals. A bare word "service" is left
	// alone, because substituting it would also hit "services" and "Server".
	if got := read(t, dir, "frontend/index.html"); !strings.Contains(got, "<title>service</title>") {
		t.Errorf("a bare word was substituted; the rewrite must be anchored:\n%s", got)
	}
}

func TestInitMarksHooksExecutable(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())
	info, err := os.Stat(filepath.Join(dir, ".githooks", "pre-commit"))
	if err != nil {
		t.Fatalf("stat the hook: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the hook is not executable, mode %v", info.Mode().Perm())
	}
}

func TestInitLibraryProfileOmitsServiceFiles(t *testing.T) {
	cfg := testConfig()
	cfg.Profile = ProfileLibrary
	cfg.Features = map[string]bool{}
	dir := initRepo(t, skeletonFS(t), cfg)

	if !exists(t, dir, "lib/doc.go") {
		t.Error("the library profile did not write its importable package")
	}
	for _, unwanted := range []string{"deploy/service.yaml", "cmd/widget/main.go", "internal/version/version.go"} {
		if exists(t, dir, unwanted) {
			t.Errorf("the library profile wrote %s", unwanted)
		}
	}
}

func TestInitRefusesASecondScaffold(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	if _, err := Init(src, dir, testConfig()); err == nil {
		t.Fatal("a second init overwrote an existing repository")
	}
}

func TestGenerationIsDeterministic(t *testing.T) {
	src := skeletonFS(t)
	first := initRepo(t, src, testConfig())
	second := initRepo(t, src, testConfig())

	a, b := tree(t, first), tree(t, second)
	if len(a) != len(b) {
		t.Fatalf("two generations wrote %d and %d files", len(a), len(b))
	}
	for path, content := range a {
		other, ok := b[path]
		if !ok {
			t.Errorf("the second generation is missing %s", path)
			continue
		}
		if content != other {
			t.Errorf("%s differs between two generations", path)
		}
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	before := tree(t, dir)

	cfg, lock, err := load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	report, err := Sync(src, dir, cfg, lock)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(report.Created)+len(report.Updated)+len(report.Removed) != 0 {
		t.Errorf("a second sync changed files: %+v", report)
	}
	after := tree(t, dir)
	for path, content := range before {
		if after[path] != content {
			t.Errorf("%s changed on a second sync", path)
		}
	}
}

func TestCheckIsCleanAfterInit(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	code, out, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("check exited %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitOK, out, errOut)
	}
	mustContain(t, out, "clean", "the clean summary")
}

func TestEditedGeneratedFileExitsThreeWithADiff(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	write(t, dir, ".golangci.yml", "version: \"2\"\nlinters:\n  default: all\n")

	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitEdited {
		t.Fatalf("check exited %d, want %d\n%s", code, ExitEdited, errOut)
	}
	mustContain(t, errOut, "edited: .golangci.yml", "the edited verdict")
	mustContain(t, errOut, "+  default: all", "the diff of the local edit")
	mustContain(t, errOut, "revert the change or send it upstream", "the remedy")
}

func TestDeletedGeneratedFileIsEdited(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	if err := os.Remove(filepath.Join(dir, ".golangci.yml")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitEdited {
		t.Fatalf("check exited %d, want %d\n%s", code, ExitEdited, errOut)
	}
	mustContain(t, errOut, "the repository deleted this file", "the deletion detail")
}

func TestBehindTemplateExitsFour(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())

	moved := mutableSkeleton(t)
	if err := os.WriteFile(filepath.Join(moved, ".golangci.yml"),
		[]byte("version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n    - gosec\n"), 0o644); err != nil {
		t.Fatalf("move the template forward: %v", err)
	}

	code, _, errOut := runCLI(t, os.DirFS(moved), "check", "-C", dir)
	if code != ExitBehind {
		t.Fatalf("check exited %d, want %d\n%s", code, ExitBehind, errOut)
	}
	mustContain(t, errOut, "behind: .golangci.yml", "the behind verdict")
	mustContain(t, errOut, "run template upgrade", "the remedy")
}

func TestANewTemplateFileIsBehindNotEdited(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())

	moved := mutableSkeleton(t)
	if err := os.WriteFile(filepath.Join(moved, "manifests", "extra.yaml"),
		[]byte("files:\n  - path: .editorconfig\n    mode: generated\n"), 0o644); err != nil {
		t.Fatalf("add a fragment: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moved, ".editorconfig"),
		[]byte("root = true\n"), 0o644); err != nil {
		t.Fatalf("add a file: %v", err)
	}

	code, _, errOut := runCLI(t, os.DirFS(moved), "check", "-C", dir)
	if code != ExitBehind {
		t.Fatalf("check exited %d, want %d\n%s", code, ExitBehind, errOut)
	}
	mustContain(t, errOut, "the template added this file", "the added-file detail")
}

func TestEditedAndBehindReportsEdited(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())
	write(t, dir, ".golangci.yml", "version: \"2\"\nlinters:\n  default: all\n")

	moved := mutableSkeleton(t)
	if err := os.WriteFile(filepath.Join(moved, ".golangci.yml"),
		[]byte("version: \"2\"\nlinters:\n  default: none\n  enable:\n    - gosec\n"), 0o644); err != nil {
		t.Fatalf("move the template forward: %v", err)
	}

	code, _, errOut := runCLI(t, os.DirFS(moved), "check", "-C", dir)
	if code != ExitEdited {
		t.Fatalf("a repository that is edited and behind exited %d, want %d\n%s", code, ExitEdited, errOut)
	}
}

func TestMergedFileKeepsConsumerContent(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())

	makefile := read(t, dir, "Makefile")
	consumerTop := "# consumer header\nDOCKER := docker\n\n"
	consumerBottom := "\n.PHONY: seed-db\nseed-db:\n\t./scripts/seed.sh\n"
	write(t, dir, "Makefile", consumerTop+makefile+consumerBottom)

	moved := mutableSkeleton(t)
	body, err := os.ReadFile(filepath.Join(moved, "Makefile.tmpl"))
	if err != nil {
		t.Fatalf("read the skeleton Makefile: %v", err)
	}
	updated := strings.Replace(string(body), "template check", "template check -C .", 1)
	if updated == string(body) {
		t.Fatal("the fixture Makefile no longer holds the line the test moves")
	}
	if err := os.WriteFile(filepath.Join(moved, "Makefile.tmpl"), []byte(updated), 0o644); err != nil {
		t.Fatalf("move the template forward: %v", err)
	}

	code, _, errOut := runCLI(t, os.DirFS(moved), "sync", "-C", dir)
	if code != ExitOK {
		t.Fatalf("sync exited %d\n%s", code, errOut)
	}

	got := read(t, dir, "Makefile")
	mustContain(t, got, "# consumer header", "consumer text above the region")
	mustContain(t, got, "seed-db:", "consumer text below the region")
	mustContain(t, got, "template check -C .", "the rewritten managed region")

	code, out, errOut := runCLI(t, os.DirFS(moved), "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("check after a merged sync exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestConsumerTextOutsideTheRegionIsNotDrift(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	write(t, dir, "Makefile", read(t, dir, "Makefile")+"\n.PHONY: local\nlocal:\n\techo local\n")

	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("consumer text outside the managed region reported drift, exit %d\n%s", code, errOut)
	}
}

func TestEditingTheManagedRegionIsDrift(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	write(t, dir, "Makefile", strings.Replace(read(t, dir, "Makefile"), "template check", "true", 1))

	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitEdited {
		t.Fatalf("an edited managed region exited %d, want %d\n%s", code, ExitEdited, errOut)
	}
}

func TestMergedFileWithoutMarkersFailsTheCheck(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	stripped := []string{}
	for line := range strings.SplitSeq(read(t, dir, "Makefile"), "\n") {
		if line == MarkerStart || line == MarkerEnd {
			continue
		}
		stripped = append(stripped, line)
	}
	write(t, dir, "Makefile", strings.Join(stripped, "\n"))

	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitError {
		t.Fatalf("a Makefile with no managed region exited %d, want %d\n%s", code, ExitError, errOut)
	}
	mustContain(t, errOut, "carries no managed region", "the marker failure reason")
}

func TestWaiverSuppressesTheEditedVerdict(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	write(t, dir, ".golangci.yml", "version: \"2\"\nlinters:\n  default: all\n")
	addWaiver(t, dir, ".golangci.yml", "one rule this repository cannot satisfy yet", "2026-12-01")

	code, out, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("a waived file exited %d, want %d\n%s", code, ExitOK, errOut)
	}
	mustContain(t, out, "waived: .golangci.yml", "the waiver report")
	mustContain(t, out, "one rule this repository cannot satisfy yet", "the waiver reason")
}

func TestExpiredWaiverFails(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	write(t, dir, ".golangci.yml", "version: \"2\"\nlinters:\n  default: all\n")
	addWaiver(t, dir, ".golangci.yml", "one rule this repository cannot satisfy yet", "2026-01-01")

	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitError {
		t.Fatalf("an expired waiver exited %d, want %d\n%s", code, ExitError, errOut)
	}
	mustContain(t, errOut, "expired waiver: .golangci.yml", "the expiry failure")
}

func TestWaiverDoesNotSuppressBehind(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())
	addWaiver(t, dir, ".golangci.yml", "a rule this repository cannot satisfy yet", "2026-12-01")

	moved := mutableSkeleton(t)
	if err := os.WriteFile(filepath.Join(moved, ".golangci.yml"),
		[]byte("version: \"2\"\nlinters:\n  default: none\n  enable:\n    - gosec\n"), 0o644); err != nil {
		t.Fatalf("move the template forward: %v", err)
	}

	code, _, errOut := runCLI(t, os.DirFS(moved), "check", "-C", dir)
	if code != ExitBehind {
		t.Fatalf("a waiver hid the behind verdict, exit %d\n%s", code, errOut)
	}
}

func TestDisablingAFeatureRemovesItsFiles(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	if !exists(t, dir, "internal/store/store.go") {
		t.Fatal("the database flag did not write its file")
	}
	setFeature(t, dir, FeatureDatabase, false)

	code, out, errOut := runCLI(t, src, "sync", "-C", dir)
	if code != ExitOK {
		t.Fatalf("sync exited %d\n%s", code, errOut)
	}
	mustContain(t, out, "internal/store/store.go", "the removal report")
	if exists(t, dir, "internal/store/store.go") {
		t.Error("disabling the flag left its generated file behind")
	}
	lock, err := LoadLock(dir)
	if err != nil {
		t.Fatalf("load the lock: %v", err)
	}
	if _, ok := lock.Entry("internal/store/store.go"); ok {
		t.Error("the lock still records the removed file")
	}

	code, _, errOut = runCLI(t, src, "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("check after the removal exited %d\n%s", code, errOut)
	}
}

func TestADeselectedFileStillOnDiskIsBehind(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	setFeature(t, dir, FeatureDatabase, false)

	code, _, errOut := runCLI(t, src, "check", "-C", dir)
	if code != ExitBehind {
		t.Fatalf("a deselected file left on disk exited %d, want %d\n%s", code, ExitBehind, errOut)
	}
	mustContain(t, errOut, "no longer selects this file", "the deselection detail")
}

func TestUpgradeMovesTheVersionAndPrintsTheDiff(t *testing.T) {
	dir := initRepo(t, skeletonFS(t), testConfig())
	seedBefore := read(t, dir, "README.md")
	write(t, dir, "README.md", seedBefore+"\nConsumer notes.\n")
	seedAfterEdit := read(t, dir, "README.md")

	moved := mutableSkeleton(t)
	if err := os.WriteFile(filepath.Join(moved, ".golangci.yml"),
		[]byte("version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n    - gosec\n"), 0o644); err != nil {
		t.Fatalf("move the template forward: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moved, "README.md.tmpl"),
		[]byte("# {{ .Name }}\n\nA rewritten seed the consumer never sees.\n"), 0o644); err != nil {
		t.Fatalf("move the seed forward: %v", err)
	}

	code, out, errOut := runCLI(t, os.DirFS(moved), "upgrade", "-C", dir, "-version", "v1.5.0")
	if code != ExitOK {
		t.Fatalf("upgrade exited %d\n%s", code, errOut)
	}
	mustContain(t, out, "from v1.4.0 to v1.5.0", "the version move")
	mustContain(t, out, "+    - gosec", "the diff of the upgraded file")

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("reload the declaration: %v", err)
	}
	if cfg.Version != "v1.5.0" {
		t.Errorf("the declaration records %s, want v1.5.0", cfg.Version)
	}
	mustContain(t, read(t, dir, ".golangci.yml"), "gosec", "the upgraded generated file")
	if got := read(t, dir, "README.md"); got != seedAfterEdit {
		t.Errorf("upgrade rewrote a seed file:\n%s", got)
	}

	code, _, errOut = runCLI(t, os.DirFS(moved), "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("check after upgrade exited %d\n%s", code, errOut)
	}
}

func TestUpgradePreservesDeclarationComments(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	original := read(t, dir, ConfigFile)
	write(t, dir, ConfigFile, "# hand written note\n"+original)

	if code, _, errOut := runCLI(t, src, "upgrade", "-C", dir, "-version", "v2.0.0"); code != ExitOK {
		t.Fatalf("upgrade exited %d\n%s", code, errOut)
	}
	got := read(t, dir, ConfigFile)
	mustContain(t, got, "# hand written note", "the consumer comment")
	mustContain(t, got, "version: v2.0.0", "the moved version")
}

func TestProfileChangeIsRejected(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	write(t, dir, ConfigFile,
		strings.Replace(read(t, dir, ConfigFile), "profile: service", "profile: library", 1))
	// A library supports no feature flags, so the declaration also has to drop
	// them for the profile guard to be the failure under test.
	write(t, dir, ConfigFile,
		strings.NewReplacer("frontend: true", "frontend: false", "database: true", "database: false").
			Replace(read(t, dir, ConfigFile)))

	for _, command := range []string{"check", "sync"} {
		code, _, errOut := runCLI(t, src, command, "-C", dir)
		if code != ExitError {
			t.Errorf("%s accepted a profile change, exit %d", command, code)
		}
		mustContain(t, errOut, "a profile change means a new scaffold", command+" refusal")
	}
}

func TestUnsupportedFeatureFlagNamesTheProfile(t *testing.T) {
	cfg := testConfig()
	cfg.Profile = ProfileLibrary
	cfg.Features = map[string]bool{FeatureDatabase: true}
	err := cfg.Validate(ConfigFile)
	if err == nil {
		t.Fatal("the library profile accepted the database flag")
	}
	mustContain(t, err.Error(), "the library profile does not support", "the profile name")
}

func TestCheckReportsAWaiverForNoGeneratedFile(t *testing.T) {
	src := skeletonFS(t)
	dir := initRepo(t, src, testConfig())
	addWaiver(t, dir, "docs/nothing.md", "left over from an earlier layout", "2026-12-01")

	code, out, _ := runCLI(t, src, "check", "-C", dir)
	if code != ExitOK {
		t.Fatalf("a stale waiver failed the check, exit %d", code)
	}
	mustContain(t, out, "covers no generated file", "the stale waiver warning")
}

// addWaiver appends a waiver to the declaration.
func addWaiver(t *testing.T, dir, path, reason, expires string) {
	t.Helper()
	data := read(t, dir, ConfigFile)
	entry := "waivers:\n  - path: " + path + "\n    reason: " + reason + "\n    expires: " + expires + "\n"
	if !strings.Contains(data, "waivers: []\n") {
		t.Fatalf("the declaration does not hold an empty waiver list:\n%s", data)
	}
	write(t, dir, ConfigFile, strings.Replace(data, "waivers: []\n", entry, 1))
}

// setFeature rewrites one feature flag in the declaration.
func setFeature(t *testing.T, dir, feature string, on bool) {
	t.Helper()
	data := read(t, dir, ConfigFile)
	from := "  " + feature + ": " + boolText(!on) + "\n"
	to := "  " + feature + ": " + boolText(on) + "\n"
	if !strings.Contains(data, from) {
		t.Fatalf("the declaration does not hold %q:\n%s", from, data)
	}
	write(t, dir, ConfigFile, strings.Replace(data, from, to, 1))
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
