package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"example.com/service/internal/worker"
)

// One image runs the web deployment, the worker deployment, and a one-off task,
// so the mode flags have to reach the invocation the entry point branches on.
func TestTheModeFlagsSelectWhatTheProcessDoes(t *testing.T) {
	tests := map[string]struct {
		args []string
		want invocation
	}{
		"no arguments": {nil, invocation{serve: true}},
		"serve":        {[]string{"-mode=serve"}, invocation{serve: true}},
		"work":         {[]string{"-mode=work"}, invocation{work: true}},
		"all":          {[]string{"-mode=all"}, invocation{serve: true, work: true}},
		"named job":    {[]string{"-job=backfill"}, invocation{job: "backfill"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := readWorkerInvocation(tc.args, io.Discard)
			if err != nil {
				t.Fatalf("readWorkerInvocation: %v", err)
			}
			if got != tc.want {
				t.Errorf("invocation = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAnUnknownModeIsRefused(t *testing.T) {
	if _, err := readWorkerInvocation([]string{"-mode=sometimes"}, io.Discard); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// The runner reaches the entry point through the assembly, so a worker
// deployment and a one-off task run the jobs the same registration produced.
func TestRegisteringJobsFillsTheRunnerSlots(t *testing.T) {
	a := newTestAssembly(t)
	if err := registerJobs(context.Background(), a); err != nil {
		t.Fatalf("registerJobs: %v", err)
	}
	if a.work == nil || a.runJob == nil {
		t.Fatalf("work=%v runJob=%v, want both", a.work != nil, a.runJob != nil)
	}
	if len(a.components) != 1 {
		t.Fatalf("registered %d components, want the telemetry release", len(a.components))
	}
	// The registration must release the observable gauge callback, which
	// outlives the runner unless the component stops it.
	if err := a.components[0].Stop(context.Background()); err != nil {
		t.Errorf("stopping the runner: %v", err)
	}
}

// A job the runner does not know fails by name rather than exiting zero.
func TestAnUnknownJobIsReportedByName(t *testing.T) {
	a := newTestAssembly(t)
	if err := registerJobs(context.Background(), a); err != nil {
		t.Fatalf("registerJobs: %v", err)
	}
	err := a.runJob(context.Background(), "backfill")
	if err == nil || !strings.Contains(err.Error(), "backfill") {
		t.Fatalf("error = %v, want one naming the job", err)
	}
}

// A registered command job runs through the same slot, which is what proves the
// slot is wired to the runner and not to an empty value.
func TestARegisteredJobRunsThroughTheSlot(t *testing.T) {
	a := newTestAssembly(t)
	if err := registerJobs(context.Background(), a); err != nil {
		t.Fatalf("registerJobs: %v", err)
	}
	ran := false
	runner := worker.New()
	if err := runner.Command(worker.JobFunc{
		JobName: "backfill",
		Fn:      func(context.Context) error { ran = true; return nil },
	}); err != nil {
		t.Fatalf("register the job: %v", err)
	}
	a.runJob = runner.RunOnce
	if err := a.runJob(context.Background(), "backfill"); err != nil {
		t.Fatalf("run the job: %v", err)
	}
	if !ran {
		t.Error("the job did not run")
	}
	if err := runner.Close(); err != nil {
		t.Errorf("close the runner: %v", err)
	}
}
