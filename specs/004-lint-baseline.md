---
title: "Lint baseline: one canonical golangci-lint configuration"
status: drafted
depends_on:
  - specs/001-template-contract.md
  - specs/003-formatting-and-hooks.md
affects: [skeleton/.golangci.yml, skeleton/Makefile]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Lint baseline

## Problem

Lint configuration is the file that diverges first and is noticed last. Two
repositories can both hold a `.golangci.yml`, both pass, and enforce different
rule sets: one starts from the tool defaults and adds linters, the other
disables all defaults and lists an explicit set. The same intent, for example
"request-path logs must carry the trace identifier", gets re-derived per
repository with a different exclusion path each time, so the rule fires in one
service and not in another.

## Scope

Layer 2. The generated `.golangci.yml`, the rule set, the exclusion mechanism,
and the lint targets.

## Design

### Explicit, not additive

The configuration starts from `default: none` and lists every enabled linter.
Tool default sets change between releases; an additive configuration therefore
changes meaning when the tool is upgraded, and the change is invisible in the
diff. An explicit list makes every upgrade a reviewable edit.

### Rule set

| Group | Linters | Reason |
| --- | --- | --- |
| Correctness | `govet`, `staticcheck`, `errcheck`, `ineffassign`, `unused` | Defects, not style |
| Modernization | `modernize` | Keeps the code on current standard library idiom |
| Boundaries | `depguard` | Blocks imports that break the layering the template defines |
| Logging | `sloglint` | Enforces the log calls that carry trace context |

`errcheck` runs with no blanket exclusions. An ignored error is written as an
explicit assignment with a comment, never as a silent drop.

### Scoped rules

A rule that applies to one package, such as the request-path logging rule, is
expressed once in the generated configuration with a `path-except` exclusion
anchored to a directory the layout spec fixes. Because the layout is fixed, the
same expression works in every consumer, and the rule cannot be quietly
weakened by moving code.

### Configuration timeouts

The generated file sets an explicit run timeout and includes test files.
Excluding tests from lint produces a second, lower standard for the code that
proves the first one.

### Targets

| Target | Behaviour |
| --- | --- |
| `make lint` | Full run, exits non-zero on any finding |
| `make lint-fix` | Applies auto-fixable findings |

`golangci-lint` never runs in a git hook. It takes a module-wide lock, and a
hook that blocks a parallel build gets disabled.

## Acceptance criteria

1. The generated `.golangci.yml` sets `default: none` and enables the listed
   linters and no others.
2. A fixture package with an unchecked error, a dead assignment, a stale idiom,
   a forbidden import, and a non-context log call produces exactly five findings,
   one per rule.
3. The scoped logging rule fires inside the request-path package and stays quiet
   outside it; a fixture proves both directions.
4. `make lint` is clean on the scaffold.
5. A drift test compares the consumer copy against the template copy and fails
   on a mismatch.

## Out of scope

Vulnerability scanning and CodeQL.
