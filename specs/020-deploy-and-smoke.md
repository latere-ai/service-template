---
title: Deploy and live smoke: rollout contract, health verification, and rollback
status: drafted
depends_on:
  - specs/008-service-runtime-contract.md
  - specs/019-tagging-and-release.md
affects: [.github/workflows/release.yml, skeleton/deploy/, skeleton/tools/smoke/]
created: 2026-08-23
author: changkun
trigger: foundation spec for any deployed service
---

# Deploy and live smoke

## Problem

A deploy step that reports success when the rollout command returns says nothing
about whether the new version serves traffic. The rollout can stall, the new
pods can crash-loop while the old ones keep serving, or the new version can come
up and answer with the previous bundle. Each of these looks like a successful
deploy in the pipeline log.

## Scope

Layer 1 and 2. The manifest contract, the rollout wait, the smoke script
contract, and the rollback rule.

## Design

### Manifest contract

| Path | Purpose |
| --- | --- |
| `deploy/base/` | The manifests, parameterized by an overlay |
| `deploy/<target>/` | One overlay per target, safe to re-apply |
| `deploy/bootstrap/` | One-time or immutable resources; the pipeline ignores it |

Everything the pipeline applies must be idempotent, because it applies on every
release. Resources that cannot be re-applied safely live in the ignored
directory and are applied by hand with a record.

The workload and its main container carry the service name, so the pipeline sets
the image without needing per-repository configuration.

### Rollout wait

The pipeline waits for the rollout to complete with a timeout, then asserts that
the observed generation matches the applied one and that the ready replica count
equals the desired count. On timeout it captures pod status, recent events, and
the last log lines from the failing pods into the run summary before failing, so
the failure is diagnosable from the run alone.

### Smoke script contract

The consumer provides one executable that receives its target through the
environment and:

1. Asserts readiness reports every dependency healthy.
2. Asserts the build identity endpoint reports the released version, commit, and
   entry asset.
3. Runs the consumer's own assertions against real endpoints.
4. Writes a markdown evidence block naming each assertion and its observed value.
5. Exits non-zero on any failed assertion.

The script runs against the live target, not against a port-forward, so it
exercises the ingress, the certificate, and the routing rather than only the
process.

Every assertion records the observed value. An assertion that only reports pass
or fail cannot show that the check was meaningful, and the evidence block exists
to be read later.

### Retry and timing

Smoke assertions retry with backoff for a bounded window, because a rollout
completes slightly before the load balancer converges. The window is bounded and
short, and the number of attempts appears in the evidence, so a check that
passes on the last attempt is visible rather than hidden.

### Rollback

A failed smoke rolls back to the previous released digest, waits for that
rollout, re-runs the smoke against it, and fails the release. A rollback that is
not itself verified can leave the service in a worse state than the failed
deploy.

## Acceptance criteria

1. A stalled rollout fails with pod status, events, and logs in the summary.
2. A deploy where the new version serves the previous bundle fails on the build
   identity assertion.
3. The evidence block contains every assertion with its observed value and its
   attempt count.
4. A failed smoke triggers a rollback, verifies the rollback, and fails the run.
5. Re-applying the same overlay twice produces no change.
6. The pipeline ignores the bootstrap directory; a test asserts it.
