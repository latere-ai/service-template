package main

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"example.com/service/internal/web"
)

// bundle is a built frontend tree: a shell document referencing a hashed entry
// module, which is what a bundler emits.
var bundle = fstest.MapFS{
	"index.html": {Data: []byte(
		`<!doctype html><script type="module" src="/assets/index-a1b2c3d4.js"></script>`)},
	"assets/index-a1b2c3d4.js": {Data: []byte("export{}\n")},
}

func TestTheEntryAssetIsPrintedAsOneLine(t *testing.T) {
	var out strings.Builder
	if err := run(&out, bundle); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "assets/index-a1b2c3d4.js\n" {
		t.Errorf("output = %q, want the entry asset on one line", out.String())
	}
}

// The placeholder bundle references no entry asset, so the command has to fail
// rather than print an empty value that the linker would stamp and the smoke
// assertion would compare against nothing.
func TestABundleWithoutAnEntryAssetIsRefusedWithAnInstruction(t *testing.T) {
	var out strings.Builder
	err := run(&out, fstest.MapFS{"index.html": {Data: []byte("<!doctype html><p>placeholder</p>")}})
	if err == nil {
		t.Fatal("a document with no entry script was accepted")
	}
	if !strings.Contains(err.Error(), "make frontend-embed") {
		t.Errorf("the message does not name the fix: %v", err)
	}
}

func TestAnAbsentBundleIsRefused(t *testing.T) {
	var out strings.Builder
	err := run(&out, fstest.MapFS{})
	if !errors.Is(err, web.ErrNoBundle) {
		t.Fatalf("error = %v, want the missing-bundle error", err)
	}
}

var errWrite = errors.New("write failed")

// failingWriter reports an error on every write, so the command's own output
// path is exercised rather than assumed.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestAWriteFailureIsReported(t *testing.T) {
	if err := run(failingWriter{}, bundle); !errors.Is(err, errWrite) {
		t.Fatalf("error = %v, want the write failure", err)
	}
}

// The exit code is the contract with make: a non-zero status is what makes the
// variable fall back rather than stamp an empty value.
func TestTheExitCodeReportsTheOutcome(t *testing.T) {
	var out, errOut strings.Builder
	if code := main1(&out, &errOut, bundle); code != 0 {
		t.Fatalf("exit code = %d for a built bundle, want 0", code)
	}
	if code := main1(&out, &errOut, fstest.MapFS{}); code != 1 {
		t.Fatalf("exit code = %d for an absent bundle, want 1", code)
	}
	if !strings.Contains(errOut.String(), "assethash:") {
		t.Errorf("the failure was not reported: %q", errOut.String())
	}
}
