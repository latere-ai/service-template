---
title: "Test standards and coverage gate: tiers, race, and a threshold that fails"
status: drafted
depends_on:
  - specs/002-go-module-baseline.md
affects: [skeleton/Makefile, .github/workflows/verify.yml, skeleton/test/]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Test standards and coverage gate

## Problem

A coverage target that is printed and not enforced is a preference, not a
standard. Repositories carry a `cover` target, produce a number, and never
compare it to anything.

A worse failure is the conditional test. Integration tests that need a database
commonly skip when the connection string is absent. The suite reports success,
the summary line says "ok", and nobody notices that the tests which check the
schema, the migrations, and the transaction boundaries did not run at all. A
green build then means "the unit tests passed and the rest was skipped".

## Scope

Layer 1 and 2. Test tiers, their targets, the coverage threshold, and the rule
that a required tier cannot skip itself.

## Design

### Tiers

| Tier | Target | Needs | Runs |
| --- | --- | --- | --- |
| Unit | `make test` | Nothing | Every change, with `-race` |
| Integration | `make test-integration` | Database and other services | Every change in CI, on demand locally |
| End to end | `make e2e` | The built binary and the built frontend | Every change in CI |
| Live smoke | `make smoke` | A deployed environment | After deploy, in the release pipeline |

### Required tiers cannot skip

A test that needs an external dependency reads its configuration through one
helper. The helper has two modes:

- `required`: a missing dependency calls `t.Fatal`, not `t.Skip`.
- `optional`: a missing dependency skips, and the skip is counted.

CI sets required mode. Local runs default to optional. The CI job asserts that
the skip count for required tiers is zero, so a test cannot become optional by
accident.

### Coverage

Coverage is measured across unit and integration tiers combined, because a
unit-only figure understates a service whose logic lives behind a database
boundary. Both tiers build with `-coverpkg=./...`, so the same statement block
appears in both profiles; the gate takes the union of the blocks either tier
covered rather than a sum, which would double-count.

The floor is judged per package, not as a repository average, and its value is
`cover.threshold` in `.lateregate.yaml`, set to 90 percent of statements. An
average lets a well-tested package carry an untested one and reports a number
nobody can act on.

A package that genuinely cannot be measured is named in `cover.exempt` with the
reason attached. An empty reason fails the load, because the value is the whole
point of the entry.

`-coverpkg=./...` also means a package with no test file of its own still
appears in the profile at zero. A package that produces no coverage data at all
is reported as unmeasured and fails, so a package cannot clear the floor by
being absent from the profile.

### Flake policy

A test that fails intermittently is quarantined with an issue reference and an
expiry, or deleted. Retry logic in CI is not permitted, because it converts a
real race into a slow build.

## Acceptance criteria

1. `make test` runs with `-race` and fails on a data race; a fixture proves it.
2. In required mode, a missing database makes the integration tier fail with a
   message naming the missing dependency, and never skip.
3. The CI verify job fails when a required-tier test skips.
4. The coverage gate fails a package below the configured floor and passes one
   at or above it; both directions are tested.
5. A package that produced no coverage data fails the build rather than being
   treated as passing.
6. The exemption list is explicit and each entry carries a reason; adding a
   package to it shows in the diff.

## Out of scope

The release-time live smoke, which the deploy spec defines.
