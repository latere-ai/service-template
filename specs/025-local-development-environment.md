---
title: "Local development environment: one command to a running stack"
status: drafted
depends_on:
  - specs/011-persistence-and-migrations.md
  - specs/013-frontend-baseline.md
affects: [skeleton/docker-compose.yml, skeleton/Makefile, skeleton/.env.example]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Local development environment

## Problem

The distance between a clean clone and a running service is where contributors
are lost. When the setup is a list of steps in a README, it is wrong within a
month, and each contributor debugs a different part of it. When local
dependencies differ from the deployed ones by version or configuration,
"works locally" stops being evidence.

## Scope

Layer 2. The dependency stack, the run targets, seeding, and the parity rule.

## Design

### One command

`make dev` starts the dependency stack, applies migrations, seeds data, starts
the backend with live reload, and starts the frontend development server with
the backend proxied. It is idempotent: running it against an already-running
stack converges rather than failing.

`make dev-down` stops everything and removes volumes, so a broken local state is
one command from clean. Contributors otherwise accumulate stale local state and
diagnose problems that do not exist in the code.

### Parity

Dependency images are pinned to the same major and minor versions the deployed
environment runs, and pinned by digest. A version drift between local and
deployed produces defects that reproduce nowhere, which are the most expensive
kind.

The service runs with the same configuration mechanism locally as in production.
Only the values differ, and they come from a generated `.env.example` copied to
a local file.

### Seeding

A seed command loads a small, deterministic data set that covers the states the
interface has to render: empty, typical, and boundary. Deterministic seeding
means a screenshot or a manual check is comparable between contributors.

### Ports and isolation

Ports are configurable with defaults, and the stack is namespaced by project, so
two checkouts of different services can run at once. A fixed port is a
collision waiting for the second project.

### Live reload

Backend changes rebuild and restart, preserving the dependency stack. Frontend
changes hot reload. The rebuild path uses the same build target as CI, so a
change that compiles locally compiles in CI.

## Acceptance criteria

1. `make dev` on a clean clone reaches a serving stack with no manual step
   beyond copying the example environment file.
2. Running `make dev` twice converges and does not fail.
3. `make dev-down` removes containers and volumes; a following `make dev`
   succeeds.
4. Dependency images are digest-pinned and match the deployed major and minor
   versions; a check asserts it.
5. Seeded data is byte-identical across runs.
6. Two projects run at once with no port collision.
