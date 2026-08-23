---
title: Repository settings as code: branch protection, ownership, and merge rules
status: drafted
depends_on:
  - specs/018-verify-pipeline.md
  - specs/022-generator-and-drift-check.md
affects: [skeleton/.github/settings.yml, skeleton/.github/CODEOWNERS, cmd/template/]
created: 2026-08-23
author: changkun
trigger: foundation spec; without it the verify pipeline is advisory
---

# Repository settings as code

## Problem

The verify pipeline calls its gate job "the single required check", but a
required check is repository configuration, not repository content. A consumer
can adopt every workflow in this template, wire the gate correctly, and still
merge a red branch, because nothing marked the check required.

Everything that makes the gates binding lives outside the files: branch
protection, review requirements, merge method, and who owns which paths. Set by
hand, these differ per repository and nobody notices a repository where
protection was never enabled.

## Scope

Layer 2. The declared settings, the apply path, the drift report, and the
ownership file.

## Design

### Declared settings

The generator writes a settings declaration covering:

| Area | Setting |
| --- | --- |
| Protection | Required status checks, including the gate job by name |
| Protection | Required up-to-date branch before merge |
| Protection | Linear history, no force push, no deletion of the default branch |
| Review | Required approvals, dismissal of stale approvals, owner review for owned paths |
| Merge | Squash only, with the pull request title as the commit subject |
| Hygiene | Delete branch on merge, auto-merge allowed |
| Security | Vulnerability reporting enabled, secret scanning with push protection |

Squash-only merge is what makes the commit message convention hold, and the
release version derivation reads those messages, so the merge setting and the
version derivation are one decision, not two.

### Apply path

Settings are applied by an authenticated command, either from a maintainer
workstation or from a scheduled workflow with an administrative identity. The
apply is idempotent, and a dry-run mode prints the difference between the
declaration and the live configuration.

The scheduled run reports drift rather than silently reverting it. A maintainer
may have changed a setting deliberately during an incident, and a job that
reverts it without notice is worse than one that reports it.

### Ownership

A code owners file maps paths to reviewers, generated from the profile:
workflow files and the template configuration are owned by maintainers, so the
pipeline and the template pin cannot be changed without an owner review.

### Bootstrap ordering

Protection cannot require a check that has never run. The scaffold therefore
applies protection after the first verify run completes, and `template check`
reports a repository whose gate is not a required check, so the gap is visible
rather than assumed away.

## Acceptance criteria

1. A repository with the settings applied cannot merge with a failing gate.
2. A repository with the settings applied cannot merge without the required
   approvals.
3. Dry-run prints the difference and changes nothing.
4. Applying twice produces no change.
5. The scheduled run reports drift and does not revert it.
6. `template check` fails when the gate is not a required status check on the
   default branch.
7. The code owners file requires owner review for workflow and template
   configuration changes.
