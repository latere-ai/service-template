---
title: Dependency updates: grouped automation and an upgrade path for the template
status: drafted
depends_on:
  - specs/018-verify-pipeline.md
affects: [skeleton/.github/dependabot.yml, .github/workflows/]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Dependency updates

## Problem

Without automation, dependencies are updated when something breaks or when an
advisory forces it, and the update is then large, urgent, and risky. With naive
automation, a repository receives one pull request per dependency per week, the
noise exceeds the attention available, and the pull requests are closed
unreviewed. Both outcomes leave the repository behind.

The template itself is a dependency, and it needs the same treatment: a consumer
must learn that a new template version exists.

## Scope

Layer 2 and 1. Update grouping, the automerge rule, and template version
notification.

## Design

### Grouping

| Group | Contents | Cadence | Automerge |
| --- | --- | --- | --- |
| Go patch and minor | All non-major module updates | Weekly, one request | Yes, when verify passes |
| Go major | One request per module | Weekly | No |
| Frontend patch and minor | All non-major packages | Weekly, one request | Yes, when verify passes |
| Frontend major | One request per package | Weekly | No |
| Workflow actions | All actions, pinned by digest | Weekly, one request | Yes, when verify passes |
| Security | Any advisory-driven update | Immediate, ungrouped | No |

Grouping is what makes the automation survivable. One request that moves twenty
patch versions and passes the full verify pipeline is reviewable; twenty
requests are not.

Automerge applies only when the full gate passes, and never to a major version
or a security update, because both need a human to read the change.

### Action pinning

Workflow actions are pinned by commit digest, not by tag. A tag on an action
repository can be moved by its owner, which makes a tag-pinned action a mutable
dependency inside the build.

### Template version notification

A scheduled job in each consumer compares the version in `.template.yaml`
against the latest template release. When the consumer is behind, it opens or
updates one issue naming the current version, the latest version, and the
changes between them. It does not open a pull request, because absorbing a
template version can require running the generator and reviewing generated
diffs.

### Update hygiene

An update request carries the release notes for each moved dependency in its
body and the bundle-size change from the verify summary, so review is possible
without opening every changelog.

## Acceptance criteria

1. Twenty pending patch updates produce one grouped request, not twenty.
2. A major update arrives ungrouped and does not automerge.
3. An advisory-driven update opens immediately, outside the weekly cadence.
4. Automerge fires only after the gate job passes; a forced gate failure blocks
   it.
5. Every workflow action reference is a digest; a check fails on a tag reference.
6. A consumer behind on the template version gets exactly one open issue, updated
   rather than duplicated on the next run.
