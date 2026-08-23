---
title: Generator and drift check: materializing template files and proving they match
status: drafted
depends_on:
  - specs/001-template-contract.md
  - specs/004-lint-baseline.md
affects: [cmd/template/, skeleton/]
created: 2026-08-23
author: changkun
trigger: foundation spec; the mechanism that makes the template durable
---

# Generator and drift check

## Problem

Layer 2 files live in the consumer repository, which is what makes them usable
by editors and local tooling, and also what lets them drift. A lint
configuration that was correct at scaffold time and has since been edited in one
repository is indistinguishable, by inspection, from one that is current. Nobody
audits eighteen copies of a file by hand.

Without a mechanism here, the template is a starter kit, and every earlier spec
in this deck decays.

## Scope

Layer 2. The generator command, its file model, the drift check, and the upgrade
flow.

## Design

### Command

```
template init      scaffold a new repository from a profile
template sync      rewrite generated files to the pinned version
template check     compare on-disk files against the pinned version, exit non-zero on drift
template upgrade   move .template.yaml to a new version, then sync, and print the diff
```

The command reads `.template.yaml` for the version, the profile, and the feature
flags, and writes only the files those flags select.

### File model

Each template file has a mode:

| Mode | Behaviour | Examples |
| --- | --- | --- |
| Generated | Rewritten by `sync`; drift is an error | Lint configuration, hooks, workflow callers |
| Seed | Written once at `init`; never rewritten, never checked | `main.go`, example handlers, README |
| Merged | Rewritten with marked regions; the consumer owns the rest | `Makefile`, `.gitignore` |

Merged files exist because a `Makefile` legitimately holds consumer targets. The
template owns a delimited region, the consumer owns everything outside it, and
the check compares the region only. Without this mode the template would either
own too much and block consumers, or own too little and check nothing.

### Drift check

`template check` records a content digest per generated file in a lock file next
to `.template.yaml`. Three outcomes:

- On-disk matches the template version: pass.
- On-disk differs and the lock digest matches the template: the consumer edited
  it. Report the diff and fail.
- On-disk matches the lock but the lock differs from the template: the consumer
  is behind. Report the pending upgrade and fail with a distinct code.

Distinguishing "edited" from "behind" matters because the remedies differ:
one is a revert or an upstream change, the other is an upgrade.

### Escape hatch

A consumer can waive one generated file in `.template.yaml` with a reason and an
expiry. The check reports waived files and fails on an expired waiver. Templates
without an escape hatch get forked wholesale the first time a consumer has a
real need, so the hatch is what keeps the rest of the contract intact.

### Determinism

Generation is deterministic: the same version, profile, and flags produce
byte-identical output on any machine. Non-deterministic output would make the
drift check useless, so a test generates twice and compares.

### Consumer wiring

`make template-check` runs the check, and the verify pipeline runs that target,
so drift fails CI in every consumer without per-repository setup.

## Acceptance criteria

1. `template init` on an empty directory produces a repository that passes its
   own verify pipeline.
2. Generation is byte-identical across two runs and across platforms.
3. An edited generated file fails `check` with a diff and the "edited" code.
4. A consumer pinned to an older version fails `check` with the "behind" code.
5. `upgrade` moves the version, rewrites generated files, leaves seed files
   untouched, and prints the diff.
6. A merged file keeps consumer content outside the owned region across a sync.
7. A waived file is reported and does not fail; an expired waiver fails.
8. Disabling a feature flag removes its generated files on the next sync.
