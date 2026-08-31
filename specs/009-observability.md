---
title: "Observability: traces, metrics, and trace-correlated logs"
status: drafted
depends_on:
  - specs/008-service-runtime-contract.md
  - specs/004-lint-baseline.md
affects: [skeleton/internal/observability/, skeleton/.lateregate.yaml]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Observability

## Problem

Telemetry that is adopted per service arrives in a different shape each time.
Span names differ, metric names differ, and the log-to-trace link is the part
that most often gets missed: a handler logs with a plain logger call, the record
carries no trace identifier, and the log line cannot be joined to the request it
describes. The defect is invisible in review because the code compiles, the log
appears, and only an incident reveals that the correlation is absent.

## Scope

Layer 2, 3, and the lint rule that enforces it.

## Design

### One initializer

A single function starts traces, metrics, and logs from configuration and
returns a shutdown function. It uses the OpenTelemetry protocol exporter, reads
endpoint and headers from standard environment variables, and falls back to a
no-op provider when no endpoint is configured, so a local run needs no collector.

Resource attributes are set once: service name, version, commit, instance
identifier, and deployment environment. Because the version comes from the same
package the release pipeline stamps, a trace is attributable to a build.

### Signals

| Signal | Content |
| --- | --- |
| Traces | One server span per request, client spans for outbound calls and database queries |
| Metrics | Request rate, error rate, and duration histogram per route and status class; in-flight gauge; dependency check results |
| Logs | Structured, JSON in production and text locally, exported through the logging bridge |

Route labels use the registered route pattern, never the raw path. A metric
labelled with the raw path produces one time series per identifier and overwhelms
the backend.

### Log and trace correlation

Every log record emitted while a request context is in scope carries the trace
and span identifiers. This works only when the call site passes the context, so
the request-path packages must use the context-aware logging calls.

The rule is enforced, not documented: the lint configuration requires
context-aware log calls inside the handler and middleware packages, and leaves
start-up and background logging unrestricted. The scope is expressed against the
fixed layout, so it behaves the same in every consumer.

### Sampling

Head sampling is parent-based with a configurable ratio, defaulting to full
sampling in development and a ratio in production. Errors and slow requests are
always sampled through a rule that overrides the ratio, because the traces worth
keeping are the ones that went wrong.

## Acceptance criteria

1. With no endpoint configured the service starts and emits no telemetry errors.
2. A request produces one server span whose name is the route pattern, and any
   database call inside it produces a child span.
3. A log record emitted inside a request carries the trace and span identifiers;
   a test asserts the fields.
4. A plain log call added to the handler package fails lint; the same call in a
   background package passes.
5. Metric route labels use the pattern; a test with a parameterized route
   asserts one series, not one per identifier.
6. An error response is sampled even when the ratio is set to zero.

## Out of scope

Dashboards and alert rules, which belong to the deployment environment.
