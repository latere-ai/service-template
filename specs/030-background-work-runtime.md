---
title: Background work runtime: scheduled jobs, queue consumers, and one-shot commands
status: drafted
depends_on:
  - specs/008-service-runtime-contract.md
  - specs/009-observability.md
affects: [skeleton/internal/worker/, skeleton/cmd/]
created: 2026-08-23
author: changkun
trigger: a consumer runs scheduled or queue-driven work
---

# Background work runtime

## Problem

The runtime contract covers a process that serves requests. Real services also
run work with no request behind it: a periodic reconciliation, a queue consumer,
a data backfill invoked once. Each of these is usually written as an ad hoc
goroutine inside the serving process or as a separate binary with none of the
lifecycle, telemetry, or configuration the serving path has.

Both shapes fail in known ways. A goroutine started in the serving process runs
once per replica, so a periodic task fires as many times as there are replicas.
A separate binary without the shared lifecycle leaks its work when the container
is terminated mid-task.

## Scope

Layer 2 and 3. The work interfaces, execution modes, the single-execution rule,
and the observability of work that has no request.

## Design

### The template ships no broker

Whether a message broker is worth running as its own component is a decision
per product, not a decision this template makes. It therefore ships no broker,
selects none, and does not require one.

What it ships is the shape: the interface a consumer implements, and the
lifecycle, locking, retry, and telemetry that sit around it. A consumer that
runs only scheduled work needs no broker at all and still gets the whole
mechanism. A consumer that later adopts one implements the same interface
against it, and nothing above the interface changes.

### One shape for three kinds

```go
// Job runs to completion. The context is cancelled on shutdown.
type Job interface {
    Name() string
    Run(ctx context.Context) error
}
```

A scheduled task is a `Job` with a schedule. A queue consumer is a `Job` that
loops on a receive with the context checked between messages. A one-shot command
is a `Job` invoked from the command line. One interface means one place that
handles cancellation, retry, telemetry, and error reporting.

### Execution modes

The binary takes a mode: serve, work, or run a named job once. Serve and work in
one process is allowed for small deployments and separated for larger ones,
without a code change. The mode is a flag, so the same image runs a web
deployment, a worker deployment, and a one-shot task.

### Single execution

A scheduled job acquires a lock keyed by its name before it runs and releases it
after. Two replicas therefore produce one execution. The lock has a lease
shorter than the schedule interval and is renewed while the job runs, so a
replica that dies mid-job releases the lock by expiry rather than blocking the
schedule forever.

### Shutdown

A job receives context cancellation on shutdown and has a bounded window to
stop. A queue consumer stops receiving, finishes the message in hand, and
acknowledges it. Acknowledging before the work completes loses the message on
shutdown, so acknowledgement always follows completion.

### Retry and failure

Retries use bounded exponential backoff with jitter and a maximum attempt count.
A job that exhausts its attempts records a terminal failure with the reason.
Unbounded retry against a permanently failing input is an infinite loop that
looks like activity.

### Observability without a request

Each execution opens its own trace with the job name, the attempt number, and
the trigger. Metrics cover execution count by result, duration, and time since
the last success per job. Time since last success is the signal that catches a
scheduled job that silently stopped running, which no error rate will show.

## Acceptance criteria

1. Three replicas running one scheduled job produce one execution per interval.
2. A replica killed mid-job releases the lock by lease expiry, and the next
   interval runs.
3. A queue consumer acknowledges only after the work completes; a shutdown
   mid-message leaves the message unacknowledged.
4. Cancellation stops a job inside the bounded window and records the reason.
5. Retries respect the maximum attempt count and record a terminal failure.
6. Each execution produces a trace with the job name and attempt number, and the
   time-since-last-success metric rises when a job stops running.
7. One image serves, works, and runs a named job once, selected by flag.

## Out of scope

The broker itself, its operation, and the choice between running one and using
a managed service.
