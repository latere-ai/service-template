---
title: Formatting and git hooks: gofmt, go fix, and the pre-commit contract
status: drafted
depends_on:
  - specs/002-go-module-baseline.md
affects: [skeleton/.githooks/, skeleton/Makefile]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Formatting and git hooks

## Problem

Formatting arguments waste review time, and a formatting fix mixed into a
behaviour change hides the behaviour change. Hooks solve this only when every
clone installs them, which does not happen by default: `core.hooksPath` is local
configuration and a fresh clone has none.

A second failure mode is a hook that is too slow or too broad. A hook that runs
a full lint pass takes a global lock and blocks parallel work, so developers
disable it, and then it protects nothing.

## Scope

Layer 2. Formatting rules, the pre-commit hook, and the install path.

## Design

### Hook contents

The pre-commit hook checks only what is fast and mechanical:

1. `gofmt -l` over staged Go files. Non-empty output fails the commit.
2. `go fix` over the staged packages, reporting proposed rewrites.
3. Trailing whitespace and missing final newline on staged text files.

The hook never runs a linter binary that takes a module-wide lock, and never
runs tests. Slow and semantic checks belong in CI.

`go fix` has known rough edges: some analyzers emit rewrites that do not
compile, and some apply partially and then re-propose on the next run. The hook
therefore reports and fails, and never rewrites files in place. The developer
applies the change and re-stages.

### Install path

`make hooks` sets `core.hooksPath` to `.githooks`. The scaffold README tells a
new clone to run it. CI runs the same checks through `make fmt-check`, so a
developer who skipped the hook still cannot merge unformatted code. The hook is
a fast local mirror of a CI gate, never the only place a rule exists.

### Targets

| Target | Behaviour |
| --- | --- |
| `make fmt` | Rewrites files in place |
| `make fmt-check` | Reports and exits non-zero; used by CI and the hook |
| `make hooks` | Installs the hooks path |

## Acceptance criteria

1. `make hooks` on a fresh clone makes the pre-commit hook run.
2. A commit with an unformatted staged Go file fails with the file name in the
   message.
3. `make fmt-check` exits non-zero on the same tree and zero after `make fmt`.
4. The hook completes in under two seconds on a repository with 500 Go files.
5. The hook runs no linter that takes a module-wide lock; a test asserts this.

## Out of scope

Lint rules and static analysis.
