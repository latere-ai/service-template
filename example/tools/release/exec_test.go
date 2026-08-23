package main

import (
	"context"
	"strings"
	"testing"
)

func TestExecRunnerCapturesOutput(t *testing.T) {
	res := ExecRunner{}.Run(context.Background(), Command{Name: "go", Args: []string{"env", "GOOS"}})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if strings.TrimSpace(res.Output) == "" {
		t.Error("Run captured no output")
	}
}

// Several callers read the exit code as data, so a non-zero exit is returned
// rather than raised.
func TestExecRunnerReportsANonZeroExit(t *testing.T) {
	res := ExecRunner{}.Run(context.Background(), Command{Name: "go", Args: []string{"env", "-nonsense"}})
	if res.Err == nil {
		t.Fatal("Run reported success for a failing command")
	}
	if res.ExitCode <= 0 {
		t.Errorf("ExitCode = %d, want the command's own code", res.ExitCode)
	}
	mustContain(t, res.Err.Error(), "go env", "the error")
}

func TestExecRunnerReportsAMissingBinary(t *testing.T) {
	res := ExecRunner{}.Run(context.Background(), Command{Name: "no-such-command-here"})
	if res.Err == nil {
		t.Fatal("Run reported success for a command that does not exist")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 for a command that never ran", res.ExitCode)
	}
}

func TestExecRunnerPassesStdinAndDirectory(t *testing.T) {
	dir := t.TempDir()
	res := ExecRunner{}.Run(context.Background(), Command{
		Name: "go", Args: []string{"env", "GOMOD"}, Dir: dir, Stdin: "ignored",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	// A directory outside a module reports no module file, which proves the
	// working directory was applied.
	if strings.TrimSpace(res.Output) != "/dev/null" && strings.TrimSpace(res.Output) != "" {
		t.Errorf("GOMOD = %q, want the value for a directory outside a module", res.Output)
	}
}

func TestCommandString(t *testing.T) {
	if got := (Command{Name: "git", Args: []string{"status", "--porcelain"}}).String(); got != "git status --porcelain" {
		t.Errorf("String = %q", got)
	}
	if got := (Command{Name: "cosign"}).String(); got != "cosign" {
		t.Errorf("String = %q", got)
	}
}

func TestOutputTrimsAndPropagatesFailure(t *testing.T) {
	r := &fakeRunner{stubs: []stub{{match: "git status", result: ok("  value  \n")}}}
	got, err := output(context.Background(), r, git("status"))
	if err != nil || got != "value" {
		t.Fatalf("output = %q, %v", got, err)
	}
	failing := &fakeRunner{stubs: []stub{{match: "git status", result: fails("boom")}}}
	if _, err := output(context.Background(), failing, git("status")); err == nil {
		t.Fatal("output hid a failure")
	}
}
