package main

import (
	"strings"
	"testing"
)

func TestRunValidatesWritesAndVerifies(t *testing.T) {
	dir := writeSpecs(t, specFile{"001-a.md", full("A", "drafted", "", "")})

	var out, errOut strings.Builder
	if err := run([]string{"-dir", dir}, &out, &errOut); err == nil {
		t.Fatal("the command passed with no index")
	}

	out.Reset()
	if err := run([]string{"-dir", dir, "-write-index"}, &out, &errOut); err != nil {
		t.Fatalf("write the index: %v", err)
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Errorf("the command did not report the write: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"-dir", dir}, &out, &errOut); err != nil {
		t.Fatalf("verify the index: %v", err)
	}
	if !strings.Contains(out.String(), "index current") {
		t.Errorf("the command did not report the verification: %q", out.String())
	}
}

func TestRunReportsEveryProblemAtOnce(t *testing.T) {
	dir := writeSpecs(t,
		specFile{"001-a.md", full("A", "started", "", "")},
		specFile{"002-b.md", full("B", "complete", "", "")},
	)
	var out, errOut strings.Builder
	err := run([]string{"-dir", dir}, &out, &errOut)
	if err == nil {
		t.Fatal("an invalid directory passed validation")
	}
	message := err.Error()
	if !strings.Contains(message, "is not one of") || !strings.Contains(message, "no Outcome section") {
		t.Errorf("the command stopped at the first problem: %v", message)
	}
}

func TestRunRejectsAnUnknownFlag(t *testing.T) {
	var out, errOut strings.Builder
	if err := run([]string{"-nope"}, &out, &errOut); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

// The directory this repository ships must pass its own validator, so a
// generated repository starts with a spec directory that is already valid.
func TestShippedSpecDirectoryIsValid(t *testing.T) {
	var out, errOut strings.Builder
	if err := run([]string{"-dir", "../../specs"}, &out, &errOut); err != nil {
		t.Fatalf("the committed spec directory does not pass validation: %v", err)
	}
}
