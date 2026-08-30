---
title: "Persistence and migrations: schema versioning and a test database that must exist"
status: drafted
depends_on:
  - specs/006-test-standards-and-coverage-gate.md
  - specs/007-configuration-and-secrets.md
affects: [skeleton/internal/store/, skeleton/migrations/, .github/workflows/verify.yml]
created: 2026-08-23
author: changkun
trigger: a consumer sets features.database in .template.yaml
---

# Persistence and migrations

## Problem

Some services carry a migrations directory and some do not, and the ones that do
apply migrations differently: by hand, at start-up, or as a separate job. The
choice matters during a rolling deploy, when the old and new code both run
against one schema.

The test side is worse. Database-backed tests commonly skip without a connection
string, so the suite passes while the queries, the constraints, and the
migrations are never exercised.

## Scope

Layer 2 and 3, active when the database feature is enabled. Migration format,
apply strategy, connection management, and the test database contract.

## Design

### Migration format

Numbered, forward-only SQL files with an optional down file for local use:

```
migrations/0001_create_users.up.sql
migrations/0001_create_users.down.sql
```

The applied set is tracked in a table with the file digest, so an edited
migration that was already applied is detected and rejected rather than silently
ignored.

### Apply strategy

Migrations run as a separate step before the new version starts, never from the
serving process. Two replicas starting at once would otherwise race, and a
start-up migration couples schema change to rollout timing.

Because the old and new code overlap during a rolling deploy, every migration
must be backward compatible with the previous release. A rename is therefore
three releases: add the new column and write both, backfill and read the new
one, then drop the old one. The spec records this as a rule the review checks,
because the failure only appears in production.

An advisory lock guards the apply step, so a concurrent run waits instead of
corrupting the tracking table.

### Connection management

One pool, configured with maximum connections, maximum lifetime, and an
acquisition timeout. Every query takes a context. A query with no timeout holds
a pool slot until the server answers, and a slow dependency then exhausts the
pool and takes the whole service down.

### Test database contract

Integration tests use the required-dependency helper. When the database is
unreachable in CI mode, the test fails and names the missing dependency. It
never skips.

Each test package runs against a fresh schema created from the migration files,
not from a hand-maintained fixture schema, so a migration that does not apply
cleanly fails the test run. Tests run in a transaction that rolls back, or
against a per-test schema, so they can run in parallel.

## Acceptance criteria

1. Applying migrations twice is a no-op; applying an edited already-applied
   migration fails with a digest mismatch.
2. Two concurrent apply runs serialize through the advisory lock and both report
   success.
3. In CI mode an unreachable database fails the integration tier with a message
   naming the dependency, and never skips.
4. The test schema is built from the migration files; a broken migration fails
   the test run.
5. A query whose context is cancelled releases its pool slot; a test asserts the
   pool returns to its idle size.
6. A migration that drops a column referenced by the previous release is
   rejected by the compatibility check.

## Out of scope

The choice of database engine beyond the generated default, and data backup.
