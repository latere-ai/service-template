# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

### Added

- The `template` command carries the skeleton of its own release, so
  `go run latere.ai/x/service-template/cmd/template@<version>` scaffolds,
  syncs, and checks a service with no checkout of this repository. A build
  that carries its skeleton refuses to record, sync to, or check against any
  other release, and prints the `go run` command for the release that can.
  `-skeleton` and `TEMPLATE_SKELETON` still name a tree on disk; the working
  directory no longer selects one.

- `docs/adopting.md`: scaffolding a service, the files it owns and the files
  the template owns, the first changes, wiring the four pipelines, and
  keeping a service current with `check`, `sync`, `upgrade`, and waivers.
- A new service's `CONTRIBUTING.md` says which of its files the template owns
  and what to do about each mode. Seed text, so it reaches services
  scaffolded from this release on.

### Fixed

- `template sync` and `template upgrade` keep a generated file the service
  edited while a live waiver covers it, instead of overwriting it, and print
  the template's change to the file beside the report. An expired waiver
  stops them before they write anything.
- `make build`, and `make dev` with it, works in a new repository before its
  first commit. The build stamped the commit as `HEAD unknown`, and the
  second word broke the link flags.
- `init` and `upgrade` without `-version` on a build that knows no release
  say why and name the builds that do: a release run with
  `go run latere.ai/x/service-template/cmd/template@latest`, or a clean,
  pushed checkout built with `go build`, whose stamped pseudo-version the
  module proxy resolves like a release. A plain `go run` in a checkout and a
  checkout with uncommitted changes have none. The error used to blame a
  `.template.yaml` nobody had written.

### Changed

- `make template-check` runs the template release `.template.yaml` declares,
  as `go run latere.ai/x/service-template/cmd/template@<version> check`, so
  the drift step of the verify pipeline works in any service, with no install
  and no checkout of the template. It used to name a module path and a tag
  that do not exist. `TEMPLATE_COMMAND` names another build of the command.
  The upgrade instructions the pipelines print name the same command.
- `.env.example` is a seed file: the service owns it, as it owns the
  configuration struct it is derived from. Adding a setting and running
  `make env-example` no longer fails `template check` with exit 3, and no
  waiver is needed. `template check` warns about a waiver that covers a file
  the service owns; remove a waiver declared for `.env.example`.
- The verify and release pipelines accept `.template.yaml` versions from
  v1.0.0, the first release; no earlier version names a release the drift
  check can run. A service scaffolded from a checkout before it runs
  `go run latere.ai/x/service-template/cmd/template@v1.0.0 upgrade`.

- The skeleton serves its probes through `latere.ai/x/pkg/health` (pkg
  v0.58.0): `/livez` and `/readyz` answer `ok` as text, `/readyz` names
  each failing dependency as `not ready: <check>: <error>`, and `/version`
  reports `version`, `commit`, and `build_time`. `/healthz` answers as
  `/livez` for one release and is removed in the next. The smoke tool reads
  the new bodies and pins the entry asset from the served document alone.
