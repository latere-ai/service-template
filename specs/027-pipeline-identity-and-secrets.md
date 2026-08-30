---
title: "Pipeline identity and secrets: federated credentials and least privilege"
status: drafted
depends_on:
  - specs/017-container-image-and-supply-chain.md
  - specs/020-deploy-and-smoke.md
affects: [.github/workflows/, docs/pipeline-identity.md]
created: 2026-08-23
author: changkun
trigger: foundation spec for any pipeline that pushes an image or reaches a cluster
---

# Pipeline identity and secrets

## Problem

The release pipeline pushes to a registry, reaches a cluster, and reads
deployment credentials. How it authenticates is the part most often left to each
repository, and the default answer is a long-lived token pasted into repository
secrets. That token is broadly scoped because narrowing it is work, it never
expires because rotating it is work, and it is readable by every workflow in the
repository including one added by a contributor.

The second failure is discovery. A consumer adopting the template cannot tell
which secrets it must set until a release fails halfway through, after the image
is already pushed.

## Scope

Layer 1 and 2. The identity model, the permission model, the declared secret
set, and the preflight that proves credentials work before anything is
published.

## Design

### Federated identity, not stored tokens

The pipeline authenticates to the registry, the cluster, and the signing service
through short-lived, workflow-issued credentials exchanged for a scoped token at
run time. Nothing long-lived is stored in the repository for these three.

The trust policy on the receiving side is scoped to the repository, the
workflow file, and the ref pattern. A policy scoped to the organization alone
lets any repository in it assume the identity, which removes the point of the
exercise.

Where a service genuinely has no federation support, the fallback is a
per-purpose credential with the narrowest available grant, an expiry date
recorded in the template configuration, and a check that fails the build when
the recorded expiry is within thirty days. A credential with no expiry is not
accepted.

### Permission model

| Job | Needs | Everything else |
| --- | --- | --- |
| verify | Read the repository | Denied |
| build and push | Write packages, read the repository | Denied |
| deploy | The cluster identity, scoped to one namespace | Denied |
| publish release | Write releases | Denied |

Workflow permissions default to read-only at the top and are granted per job.
A reusable workflow cannot exceed the permissions its caller grants, so the
caller grants the minimum and the documentation states exactly which lines are
required and why.

Deployment credentials are scoped to one namespace and to the verbs the rollout
needs. A cluster-wide administrator credential in a release pipeline turns any
pipeline compromise into a cluster compromise.

### Declared secret set

The template declares every secret a profile needs, with its purpose, its
scope, and whether it is federated or stored. `template check` compares the
declaration against the repository configuration and reports what is missing,
so a consumer learns about a missing credential during setup rather than during
a release.

### Preflight

Before the first mutating step, the pipeline verifies every credential it will
use: registry write, cluster reachability with the intended verbs, and signing
availability. It fails there. A pipeline that discovers a missing deploy
credential after pushing an image leaves a published artifact with no
corresponding release.

### Forked contributions

Workflows triggered by a fork run without secrets and without the deployment
identity. The verify pipeline is therefore designed to pass with read-only
permissions, so a contributor gets a real signal without the repository lending
its identity to unreviewed code.

## Acceptance criteria

1. No long-lived registry, cluster, or signing credential is stored in a
   consumer repository for the federated path.
2. The trust policy is scoped to repository, workflow, and ref; a run from
   another repository or another workflow is rejected, and a test proves it.
3. Each job runs with the minimum permission set; a job attempting an
   out-of-scope action fails.
4. The deploy credential cannot act outside its namespace; a test asserts the
   denial.
5. `template check` reports every missing declared secret by name and purpose.
6. Preflight fails before any mutating step when a credential is missing or
   insufficient.
7. A stored fallback credential within thirty days of its recorded expiry fails
   the build.
8. The verify pipeline passes for a fork with no secrets.
