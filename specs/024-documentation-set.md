---
title: Documentation set: audience-separated docs and an accuracy check
status: drafted
depends_on:
  - specs/023-spec-driven-workflow.md
affects: [skeleton/README.md, skeleton/docs/, skeleton/CONTRIBUTING.md]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Documentation set

## Problem

Documentation fails in two directions. It goes stale, because nothing checks it
against the code, so a documented flag that was renamed misleads every reader
until someone tries it. And it mixes audiences: a README that opens with
internal process notes tells a prospective user nothing about what the software
does, while an interface reference written as a narrative gives a builder no
precision.

## Scope

Layer 2. The generated documentation set, the audience rule, and the accuracy
check.

## Design

### The set

| File | Audience | Answers |
| --- | --- | --- |
| `README.md` | Someone deciding whether to use it | What it is, what it does, how to run it in five minutes |
| `docs/architecture.md` | A new contributor | How the parts fit, with a diagram |
| `docs/configuration.md` | An operator | Every setting, its type, default, and effect |
| `docs/api.md` | A client builder | Endpoints, error envelope, versioning rule |
| `docs/operations.md` | Whoever is on call | Probes, signals, common failures, rollback |
| `CONTRIBUTING.md` | A contributor | Workflow, standards, commit format |
| `SECURITY.md` | A reporter | How to report, what is supported, response times |

Each file has one audience. Mixing them is what produces a document nobody
finishes.

### Audience register

User-facing documents describe value and use. Interface references are precise
and exhaustive. Code comments state what a thing is and why it exists, never
process or status. Documentation for a public repository does not cite internal
trackers, internal spec identifiers, or internal file paths, because those
references are unresolvable for most readers.

### Accuracy check

Generated from the code, not written by hand:

- The configuration document is generated from the configuration struct.
- The interface reference is generated from the route table and the machine
  readable description.
- Command help text in the README is generated from the command definitions.

A check target regenerates and compares. A drift fails the build. Documentation
that a build cannot verify is documentation that will be wrong.

### Link and diagram checks

The check also validates every internal link and every external link, and
renders every diagram, because a diagram with a syntax error usually renders as
nothing rather than as an error, and the omission is easy to miss in review.

## Acceptance criteria

1. Each generated document regenerates deterministically, and a stale copy fails
   the check.
2. A renamed configuration field fails the check until the document regenerates.
3. Every internal link resolves; a broken link fails.
4. Every diagram renders; a malformed diagram fails with its file and line.
5. The README quick start runs end to end on a clean clone in a test.
6. A grep check fails on internal tracker or spec identifiers in published
   documents.
