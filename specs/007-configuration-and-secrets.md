---
title: Configuration and secrets: typed config, precedence, and boot validation
status: drafted
depends_on:
  - specs/002-go-module-baseline.md
affects: [skeleton/internal/config/, skeleton/.env.example]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Configuration and secrets

## Problem

Configuration is the most copied and least designed part of a service. Each
repository invents its own precedence order, reads environment variables at the
point of use so a missing value surfaces on the first request instead of at
boot, and prints the whole configuration at start-up including the database
password.

Three concrete defects follow: a service starts with an invalid configuration
and fails later under load, a secret reaches the log aggregator, and nobody can
answer "what does this service need to run" without reading the source.

## Scope

Layer 2 and 3. The configuration type, the load order, validation, redaction,
and the generated example file.

## Design

### One type, loaded once

Configuration is a single struct in `internal/config`, loaded once in `main`,
and passed down explicitly. No package reads the environment directly. This
makes configuration testable without process-level state and makes the full set
of inputs greppable in one file.

```go
type Config struct {
    Addr        string        `env:"ADDR" default:":8080"`
    LogLevel    slog.Level    `env:"LOG_LEVEL" default:"info"`
    DatabaseURL Secret        `env:"DATABASE_URL" required:"true"`
    Timeout     time.Duration `env:"HTTP_TIMEOUT" default:"30s"`
}
```

### Precedence

Highest wins:

1. Command-line flag
2. Environment variable
3. File referenced by `<NAME>_FILE`, for container secret mounts
4. Declared default

The `_FILE` indirection matters because orchestrators mount secrets as files,
and reading the value from a file keeps it out of the process environment, where
any child process and any crash dump can see it.

### Secret type

`Secret` is a distinct string type whose `String`, `MarshalJSON`, and
`LogValue` methods return `[redacted]`. Redaction is therefore a property of the
type, not a rule each log call must remember. The underlying value is reachable
only through an explicit accessor, so every use is visible in review.

### Boot validation

`Load` returns all validation errors at once, not the first one. A service that
reports one missing variable per restart wastes a deployment cycle per error.
Validation covers required fields, parse failures, and cross-field rules.

On success the service logs the effective configuration with secrets redacted
and marks which values came from a default, so an operator can see what the
process actually resolved.

### Generated example

The generator derives `.env.example` from the struct tags. The file cannot drift
from the code because it is not hand-written, and a check target proves the
committed copy matches the current struct.

## Acceptance criteria

1. Precedence is tested for all four sources, including `_FILE`.
2. A missing required value fails `Load`, and the error names every missing
   field, not only the first.
3. A `Secret` renders as `[redacted]` through `fmt`, JSON marshalling, and
   structured logging; a test asserts the raw value appears in none of them.
4. The start-up log line shows the effective configuration with secrets redacted
   and marks defaulted values.
5. `.env.example` regenerates deterministically, and a stale committed copy
   fails the check target.

## Out of scope

Secret storage and rotation in the deployment environment.
