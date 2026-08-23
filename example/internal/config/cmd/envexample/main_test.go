package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/reference-service/internal/config"
)

func TestRunWritesThenVerifies(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.EnvExampleName)
	var out bytes.Buffer

	if err := run([]string{"-out", path, "-check"}, &out); err == nil {
		t.Fatal("the check passed with no file on disk")
	}

	out.Reset()
	if err := run([]string{"-out", path}, &out); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(out.String(), "wrote "+path) {
		t.Errorf("the command reported %q", out.String())
	}

	out.Reset()
	if err := run([]string{"-out", path, "-check"}, &out); err != nil {
		t.Fatalf("the check failed on a freshly written file: %v", err)
	}
	if !strings.Contains(out.String(), "current") {
		t.Errorf("the command reported %q", out.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written file: %v", err)
	}
	if !strings.Contains(string(data), "ADDR=") {
		t.Fatalf("the written file holds no assignments:\n%s", data)
	}

	if err := os.WriteFile(path, append(data, []byte("EXTRA=1\n")...), 0o600); err != nil {
		t.Fatalf("append to the file: %v", err)
	}
	if err := run([]string{"-out", path, "-check"}, &out); err == nil {
		t.Fatal("the check passed on an edited file")
	}
}

func TestRunRejectsAnUnknownFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-nonsense"}, &out); err == nil {
		t.Fatal("an unknown flag did not fail the command")
	}
}

func TestRunReportsAnUnwritablePath(t *testing.T) {
	var out bytes.Buffer
	path := filepath.Join(t.TempDir(), "missing", config.EnvExampleName)
	if err := run([]string{"-out", path}, &out); err == nil {
		t.Fatal("writing into a missing directory reported no error")
	}
}
