package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Command is one external invocation. The pipeline drives git, gh, and
// kubectl, and every call goes through this type so a test can state exactly
// which commands a subcommand issues and what each one returns.
type Command struct {
	Name string
	Args []string
	// Dir is the working directory, empty for the current one.
	Dir string
	// Stdin is the input the command reads, empty for none.
	Stdin string
}

// String renders the command the way a log line should show it.
func (c Command) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Result is what one command produced. Output holds standard output; ExitCode
// is meaningful only when Err is non-nil.
type Result struct {
	Output   string
	Stderr   string
	ExitCode int
	Err      error
}

// Runner executes commands. It is an interface so every subcommand is testable
// without a cluster, a remote, or a network.
type Runner interface {
	Run(ctx context.Context, c Command) Result
}

// ExecRunner runs commands on this machine.
type ExecRunner struct{}

// Run executes c and captures both streams. A non-zero exit is returned in
// Result rather than raised, because several callers read the exit code as
// data: kubectl diff reports a difference that way.
func (ExecRunner) Run(ctx context.Context, c Command) Result {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{Output: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		res.ExitCode = -1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			res.ExitCode = exitErr.ExitCode()
		}
		res.Err = fmt.Errorf("%s: %w: %s", c, err, strings.TrimSpace(stderr.String()))
	}
	return res
}

// output runs a command and returns its trimmed standard output, or the error.
func output(ctx context.Context, r Runner, c Command) (string, error) {
	res := r.Run(ctx, c)
	if res.Err != nil {
		return "", res.Err
	}
	return strings.TrimSpace(res.Output), nil
}
