---
title: Profiles: what a service, a library, and a frontend-only repository generate
status: drafted
depends_on:
  - specs/001-template-contract.md
  - specs/022-generator-and-drift-check.md
affects: [cmd/template/, skeleton/]
created: 2026-08-23
author: changkun
trigger: the contract declares three profiles; this spec defines them
---

# Profiles

## Problem

The contract declares a `profile` field with three values, and without a spec
that defines them the field is a promise the generator cannot keep. The
consequence is concrete: a library repository scaffolded from a service profile
receives deployment manifests, probe endpoints, and a smoke script it can never
use, and its owner deletes them. Deleting generated files is exactly the
drift the template exists to prevent, so an unusable generated file is not a
harmless extra.

## Scope

Layer 2. The three profiles, what each generates, and how a profile relates to
feature flags.

## Design

### Profile against feature flag

A profile selects the shape of the repository and the pipelines it can call. A
feature flag selects an optional capability inside that shape. Profile is fixed
at scaffold time because changing it is a repository rewrite; feature flags
change with a sync.

### The three profiles

| Aspect | `service` | `library` | `frontend-only` |
| --- | --- | --- | --- |
| Entry point | `cmd/<name>` binary | None; importable packages | Static site build |
| Runtime specs | 007 to 012 apply | None | None |
| Frontend specs | 013 to 016, by flag | None | 013 to 016 apply |
| Container image | Yes | No | Optional, for a static server |
| Deploy and smoke | Yes | No | Static publish and a live fetch check |
| Release output | Image and release notes | Tag, release notes, module proxy warm | Published static bundle and release notes |
| Verify jobs | All | Lint, static analysis, tests, coverage | Guard, type check, tests, build, link check |
| Documentation set | Full | Reference and quick start | Reference and quick start |

### Library specifics

A library publishes no artifact beyond the tag, so its release pipeline verifies,
tags, publishes notes, and requests the module proxy to fetch the new version so
the first consumer does not pay the cold fetch.

A library also carries a compatibility gate: an exported-interface comparison
against the previous tag that fails a minor or patch release when an exported
symbol was removed or its signature changed. This is the library equivalent of
the live smoke, the check that proves the release is safe for the people
downstream.

### Frontend-only specifics

The static bundle is published to object storage behind a content delivery
network, and the cache is invalidated for the documents while hashed assets are
left alone. The live check fetches the published entry document and asserts the
referenced asset hash matches the build, which is the same asset-pinning idea
the serving spec applies to an embedded bundle.

### Generator behaviour

The generator writes only what the profile selects. `template check` in a
library repository does not look for deploy manifests, and enabling a flag a
profile does not support fails with a message naming the profile.

## Acceptance criteria

1. Each profile scaffolds a repository that passes its own verify pipeline.
2. A library scaffold contains no deployment manifest, no probe handler, and no
   smoke script.
3. Removing an exported symbol fails a patch or minor release of a library and
   passes a major one.
4. A frontend-only release publishes the bundle, invalidates the document cache,
   and fails when the live entry document references a different asset hash.
5. Enabling an unsupported flag fails with the profile named.
6. Changing the profile in the configuration is rejected with a message that
   says a profile change means a new scaffold.
