# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

### Added

- `docs/adopting.md`: scaffolding a service, the files it owns and the files
  the template owns, the first changes, wiring the four pipelines, and
  keeping a service current with `check`, `sync`, `upgrade`, and waivers.
- A new service's `CONTRIBUTING.md` says which of its files the template owns
  and what to do about each mode, and that `.env.example` needs a waiver once
  the service adds a setting. Seed text, so it reaches services scaffolded
  from this release on.

### Changed

- The skeleton serves its probes through `latere.ai/x/pkg/health` (pkg
  v0.58.0): `/livez` and `/readyz` answer `ok` as text, `/readyz` names
  each failing dependency as `not ready: <check>: <error>`, and `/version`
  reports `version`, `commit`, and `build_time`. `/healthz` answers as
  `/livez` for one release and is removed in the next. The smoke tool reads
  the new bodies and pins the entry asset from the served document alone.
