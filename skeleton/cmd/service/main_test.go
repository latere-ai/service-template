package main

import (
	"errors"
	"strings"
	"testing"
)

func TestRunReportsTheBuild(t *testing.T) {
	var out strings.Builder
	if err := run(&out, true); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, field := range []string{"version=", "commit=", "built=", "assets="} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("the output does not carry %q: %q", field, out.String())
		}
	}
}

var errWrite = errors.New("write failed")

// failingWriter reports an error on every write, so the entry point's error
// path is exercised rather than assumed.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestRunSurfacesAWriteError(t *testing.T) {
	if err := run(failingWriter{}, false); err == nil {
		t.Fatal("run hid a write error")
	}
}
