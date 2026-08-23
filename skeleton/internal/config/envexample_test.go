package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureExample is the expected rendering of the fixture declaration. It is a
// literal rather than a golden file so a change to the format is a reviewable
// diff beside the code that produced it.
const fixtureExample = `
# Listen address.
# flag: -fx-addr
FX_ADDR=:8080

# Log level.
# flag: -fx-level
FX_LEVEL=info

# Request timeout.
# flag: -fx-timeout
FX_TIMEOUT=30s

# Retry budget.
# flag: -fx-retries
FX_RETRIES=3

# Sample ratio.
# flag: -fx-ratio
FX_RATIO=0.5

# Verbose output.
# flag: -fx-verbose
FX_VERBOSE=false

# Database password.
# flag: -fx-password
# secret: mount it as a file and set FX_PASSWORD_FILE instead.
# optional: unset leaves the feature off.
FX_PASSWORD=

# Field with an explicit flag.
# flag: -short
FX_RENAMED=x

# Field with no flag.
FX_NO_FLAG=y
`

func TestRenderEnvExampleMatchesTheDeclaration(t *testing.T) {
	specs, err := specsOf(&fixture{})
	if err != nil {
		t.Fatalf("specsOf: %v", err)
	}
	got := string(renderEnvExample(specs))
	want := envExampleHeader + fixtureExample
	if got != want {
		t.Fatalf("rendered file differs\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestEnvExampleIsDeterministic is the property the drift check depends on:
// the same struct renders the same bytes every time.
func TestEnvExampleIsDeterministic(t *testing.T) {
	first, err := EnvExample()
	if err != nil {
		t.Fatalf("EnvExample: %v", err)
	}
	for range 5 {
		next, err := EnvExample()
		if err != nil {
			t.Fatalf("EnvExample: %v", err)
		}
		if !bytes.Equal(first, next) {
			t.Fatal("two renderings of the same struct differ")
		}
	}
}

func TestEnvExampleCoversEveryDeclaredField(t *testing.T) {
	data, err := EnvExample()
	if err != nil {
		t.Fatalf("EnvExample: %v", err)
	}
	specs, err := specsOf(&Config{})
	if err != nil {
		t.Fatalf("specsOf: %v", err)
	}
	text := string(data)
	for _, s := range specs {
		if !strings.Contains(text, "\n"+s.Env+"="+s.Default+"\n") {
			t.Errorf("the example file has no assignment for %s", s.Env)
		}
		if s.Doc != "" && !strings.Contains(text, "# "+s.Doc) {
			t.Errorf("the example file omits the documentation for %s", s.Env)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		t.Error("the example file does not end with a newline")
	}
}

// TestEnvExampleSecretsCarryNoValue asserts the generated file never ships a
// secret value and always names the file indirection an orchestrator uses.
func TestEnvExampleSecretsCarryNoValue(t *testing.T) {
	data, err := EnvExample()
	if err != nil {
		t.Fatalf("EnvExample: %v", err)
	}
	specs, err := specsOf(&Config{})
	if err != nil {
		t.Fatalf("specsOf: %v", err)
	}
	text := string(data)
	for _, s := range specs {
		if !s.isSecret {
			continue
		}
		if s.Default != "" {
			t.Errorf("%s declares a default, so the example file ships a secret value", s.Env)
		}
		if !strings.Contains(text, s.Env+fileSuffix) {
			t.Errorf("the example file does not mention %s%s", s.Env, fileSuffix)
		}
	}
}

// TestCheckEnvExampleFailsOnAStaleCopy proves the gate can fail. Without it the
// check target would pass whatever the committed file holds.
func TestCheckEnvExampleFailsOnAStaleCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnvExampleName)

	if err := CheckEnvExample(path); !errors.Is(err, ErrEnvExampleStale) {
		t.Fatalf("a missing file gave %v, want the stale error", err)
	}

	if err := WriteEnvExample(path); err != nil {
		t.Fatalf("WriteEnvExample: %v", err)
	}
	if err := CheckEnvExample(path); err != nil {
		t.Fatalf("a freshly written file failed the check: %v", err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written file: %v", err)
	}
	specs, err := specsOf(&Config{})
	if err != nil {
		t.Fatalf("specsOf: %v", err)
	}
	first := specs[0]
	stale := bytes.Replace(current,
		[]byte("\n"+first.Env+"="+first.Default+"\n"),
		[]byte("\n"+first.Env+"=edited-by-hand\n"), 1)
	if bytes.Equal(stale, current) {
		t.Fatal("the test did not modify the file, so the check proves nothing")
	}
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatalf("write the stale file: %v", err)
	}

	err = CheckEnvExample(path)
	if !errors.Is(err, ErrEnvExampleStale) {
		t.Fatalf("a stale file gave %v, want the stale error", err)
	}
	if !strings.Contains(err.Error(), "edited-by-hand") {
		t.Fatalf("the failure does not name the differing line: %v", err)
	}
	if !strings.Contains(err.Error(), "make env-example") {
		t.Fatalf("the failure does not say how to fix it: %v", err)
	}
}

func TestCheckEnvExampleReportsATruncatedCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnvExampleName)
	if err := WriteEnvExample(path); err != nil {
		t.Fatalf("WriteEnvExample: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written file: %v", err)
	}
	if err := os.WriteFile(path, current[:len(current)/2], 0o600); err != nil {
		t.Fatalf("truncate the file: %v", err)
	}
	if err := CheckEnvExample(path); !errors.Is(err, ErrEnvExampleStale) {
		t.Fatalf("a truncated file gave %v, want the stale error", err)
	}
}

func TestWriteEnvExampleReportsAnUnwritablePath(t *testing.T) {
	err := WriteEnvExample(filepath.Join(t.TempDir(), "missing", EnvExampleName))
	if err == nil {
		t.Fatal("writing into a missing directory reported no error")
	}
}

func TestFirstDifferenceReportsTrailingContent(t *testing.T) {
	got := firstDifference([]byte("a\n"), []byte("a\n"))
	if !strings.Contains(got, "trailing content") {
		t.Fatalf("firstDifference = %q", got)
	}
}

// TestCommittedEnvExampleIsCurrent runs the same comparison the check target
// runs, against the copy committed at the repository root, so a struct change
// with no regenerated file fails the test tier as well as the make target.
func TestCommittedEnvExampleIsCurrent(t *testing.T) {
	path := filepath.Join("..", "..", EnvExampleName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not committed; create it with: make env-example", EnvExampleName)
	}
	if err := CheckEnvExample(path); err != nil {
		t.Fatal(err)
	}
}

func TestFirstDifferenceReportsAMissingLine(t *testing.T) {
	got := firstDifference([]byte("a\n"), []byte("a\nb\n"))
	if !strings.Contains(got, "line 2") || !strings.Contains(got, `"b"`) {
		t.Fatalf("firstDifference = %q", got)
	}
}
