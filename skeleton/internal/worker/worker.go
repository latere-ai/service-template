// Package worker runs work that has no request behind it: a periodic
// reconciliation, a queue consumer, a backfill invoked once from the command
// line.
//
// One interface covers the three shapes, so cancellation, single execution,
// retry, and telemetry are written once. A scheduled task is a [Job] with a
// schedule. A queue consumer is a [Job] that loops on a receive. A one-shot
// command is a [Job] invoked by name.
//
// The binary decides what it does with [ParseInvocation]. Serve mode registers
// the HTTP surface only, work mode calls [Runner.Run], all mode does both in one
// process, and job mode calls [Runner.RunOnce] with the name the flag carried
// and exits with its result. One image therefore runs the web deployment, the
// worker deployment, and a one-off task.
//
// The package ships no message broker and selects none. What it defines is the
// shape around one: the [Locker] a scheduled job takes its lease from and the
// [Queue] a consumer receives from are interfaces, with in-process
// implementations for a single-replica deployment and for tests. A consumer
// that adopts a broker implements those two interfaces against it, and nothing
// above them changes.
package worker

import (
	"context"
	"fmt"
	"time"
)

// Job runs to completion. The context is cancelled on shutdown, and a job that
// ignores it is stopped by force when the shutdown window expires.
//
// Name identifies the job in the lock key, the trace, the metric attributes,
// and the command-line flag that runs it once. It is stable across releases,
// because a renamed job takes a different lock and loses its history.
type Job interface {
	Name() string
	Run(ctx context.Context) error
}

// JobFunc adapts a named function to [Job].
type JobFunc struct {
	JobName string
	Fn      func(ctx context.Context) error
}

// Name reports the job name.
func (j JobFunc) Name() string { return j.JobName }

// Run calls the function.
func (j JobFunc) Run(ctx context.Context) error { return j.Fn(ctx) }

// Triggers name what started an execution. The attribute separates a schedule
// that fired from an operator who ran the same job by hand, which are read
// differently when the execution fails.
const (
	// TriggerSchedule is an execution the schedule started.
	TriggerSchedule = "schedule"
	// TriggerManual is an execution the command line started.
	TriggerManual = "manual"
	// TriggerContinuous is a long-running job the runtime started once.
	TriggerContinuous = "continuous"
)

// Execution results. Every attempt ends as exactly one of these, and the set is
// closed so the metric has one series per outcome family.
const (
	// ResultSuccess is an attempt that returned no error.
	ResultSuccess = "success"
	// ResultError is a failed attempt with a retry still to come.
	ResultError = "error"
	// ResultFailed is a terminal failure: the attempt budget is spent.
	ResultFailed = "failed"
	// ResultCancelled is an execution that stopped because the context ended.
	ResultCancelled = "cancelled"
	// ResultSkipped is a scheduled execution another replica holds the lock for.
	ResultSkipped = "skipped"
	// ResultAbandoned is a job still running when the shutdown window expired.
	ResultAbandoned = "abandoned"
)

// TerminalError reports a job that spent its attempt budget. It carries the
// last error, so the reason a job stopped retrying is the reason it failed.
type TerminalError struct {
	Job      string
	Attempts int
	Err      error
}

// Error states the job, the attempts spent, and the last failure.
func (e *TerminalError) Error() string {
	return fmt.Sprintf("worker: job %q failed after %d attempts: %v", e.Job, e.Attempts, e.Err)
}

// Unwrap exposes the last attempt's error to errors.Is and errors.As.
func (e *TerminalError) Unwrap() error { return e.Err }

// execution is one attempt's outcome, as recorded to telemetry.
type execution struct {
	job      string
	attempt  int
	trigger  string
	result   string
	duration time.Duration
	err      error
}
