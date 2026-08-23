// Command smoke asserts that a released build is serving on a live target.
//
// It runs after the rollout completes and before the release is published. It
// reads its target and the expected build identity from the environment, runs
// every assertion against the live address rather than a port forward, and
// writes a markdown evidence block naming each assertion, the value it
// observed, and how many attempts it needed.
//
// Every assertion records its observed value. An assertion that reports only
// pass or fail cannot show that the check was meaningful, and the evidence
// block exists to be read later.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Getenv, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "smoke: %v\n", err)
		os.Exit(1)
	}
}

// run reads the environment, executes every assertion, writes the evidence,
// and reports whether the target passed. The environment is a parameter so a
// test drives it without mutating the process.
func run(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: cfg.RequestTimeout}
	assertions, err := Assertions(cfg, client)
	if err != nil {
		return err
	}

	results := RunAll(ctx, assertions, cfg.Window, cfg.Backoff, time.Sleep)
	evidence := Evidence(cfg, results)

	if err := WriteEvidence(cfg.EvidencePath, stdout, evidence); err != nil {
		return err
	}
	if failed := Failures(results); len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for _, r := range failed {
			names = append(names, r.Name)
		}
		return fmt.Errorf("%d assertion(s) failed: %v", len(failed), names)
	}
	return nil
}

// WriteEvidence writes the block to path, appending when the file exists so a
// run summary that already holds other sections keeps them. An empty path
// sends the block to stdout, which is what a local run wants.
func WriteEvidence(path string, stdout io.Writer, block string) error {
	if path == "" {
		if _, err := fmt.Fprint(stdout, block); err != nil {
			return fmt.Errorf("write evidence: %w", err)
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open evidence file: %w", err)
	}
	_, writeErr := fmt.Fprint(f, block)
	if joined := errors.Join(writeErr, f.Close()); joined != nil {
		return fmt.Errorf("write evidence file %s: %w", path, joined)
	}
	return nil
}
