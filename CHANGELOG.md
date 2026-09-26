# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

A service now runs two pipelines on every push. `.github/workflows/ci.yml`
calls the fleet's shared per-push bar in `latere-ai/ci`, the gates lateregate
names for the repository at the version `go.mod` pins, and the template's
verify pipeline keeps only the checks that bar does not cover. No check runs
in both. The verify and release pipelines now accept a `.template.yaml`
version of `v1.1.0` or later, because the verify pipeline relies on the
generated `.github/workflows/ci.yml` to run the checks it no longer runs.

A v1.0.0 service moves with
`go run latere.ai/x/service-template/cmd/template@v1.1.0 upgrade`. The upgrade
writes the two new callers, the new pre-push hook, `.github/settings.yml`
with the second required context, `.lateregate.yaml`, the `make/` fragments,
and the `.gitignore` region, and writes `CHANGELOG.md` and `LICENSE` where the
service holds neither. It records `license.year` in `.template.yaml`, the year
it runs in unless `-year` names another, and writes the license notice on
every generated Go file. A declaration with no license block gets the
default terms spelled out, `LicenseRef-Proprietary` held by `Latere AI`; to
declare others, edit the block and run `sync`. Four steps are by hand,
because they touch seed files or live repository settings:

1. Pin ci-gate v0.50.1 or later, which the delegating hooks need:
   `go get -tool latere.ai/x/ci-gate/cmd/lateregate@v0.50.1`. `go.mod` is the
   service's, so the upgrade leaves it alone. The daily bump keeps the pin
   current from there.
2. Write the license notice on the Go files the service owns, which the
   upgrade does not rewrite: `go tool lateregate license -w` writes it on
   every checked file that has none, which leaves the generated files, already
   carrying the rendered notice, untouched. Review and commit.
3. Apply the declared settings, so `gate / all gates passed` becomes a
   required check beside `verify / gate`: dispatch the settings workflow with
   `mode: apply`, or run `make settings-apply`. `make settings-required-check`
   reports a context the branch does not require yet.
4. A service with a database reads `DATABASE_POOL_URL` on the serving path
   and keeps `DATABASE_URL` for migrations, as the scaffold's
   `cmd/<name>/database.go` and `internal/config/config.go` now do; both are
   seed, so port the change by hand. The `postgres` gate holds the shape.

`go tool lateregate contract` then reports the repository in shape.

### Added

- `.github/workflows/ci.yml`, a generated caller of the shared per-push bar
  whose aggregate reports as `gate / all gates passed`. It carries the
  top-level concurrency block the bar's wiring check requires, and a
  `workflow_dispatch` trigger so the daily ci-gate bump can run it on the
  commit it pushes.
- `.github/workflows/ci-gate-bump.yml`, a generated caller that moves the
  `latere.ai/x/ci-gate` pin to the latest release once a day when the whole
  bar passes on it, and opens one issue for the version when it does not. It
  runs at a minute derived from the service name, so services sharing a
  runner do not start together.
- `.githooks/pre-push`, delegating to lateregate: it lints the packages a
  push changes and refuses a release tag with no `CHANGELOG.md` section.
- `CHANGELOG.md` in every new service, seed, with an `Unreleased` heading.
- A service declares the license it is released under in `.template.yaml`,
  with `-license`, `-holder`, and `-year` on `init`. The default is
  `LicenseRef-Proprietary`, and the year defaults to the year `init` runs in.
  The generator writes the SPDX notice at the top of every Go file it
  renders, writes `LICENSE` for the licenses it ships the text of, and
  declares the terms to the license gate. The notice is ordinary rendered
  content, so `check`, `sync`, and `upgrade` treat it like any other line,
  and it names the declared year rather than the current one, so a check in
  a later year reports nothing.
- The serving path connects through a transaction-mode pooler named by
  `DATABASE_POOL_URL` and falls back to `DATABASE_URL`; the migrate command
  keeps the direct connection, which its session-scoped lock needs.
- The template's own gate scaffolds a service with no features, one with the
  frontend and the database, one with every feature, and a library, and runs
  `go tool lateregate contract` and the whole shared bar in each, so a
  skeleton that drifts from the bar's wiring, misses a license notice, or
  falls under the coverage floor for one feature set fails here rather than
  on a service's first push.
- The start-up tests open a store that needs no server, so the path after a
  successful open is covered in every feature set with a database.

### Changed

- The verify pipeline keeps the drift check, the declared settings and
  ownership, the tracked suppressions, code scanning, the integration tier
  against Postgres, the frontend, and the build the release consumes. It no
  longer runs formatting, modernization, `golangci-lint`, the spec tree,
  outbound instrumentation, `go vet`, `govulncheck`, the unit tests, the
  hermetic run, or the temporary directory run: the shared bar runs each of
  them as a lateregate gate. Its aggregate still reports as `verify / gate`.
- An advisory in `.github/suppressions.yml` no longer silences the
  vulnerability scan, which is now the bar's `vuln` gate; waive that gate in
  `.lateregate.yaml` with a reason and an expiry instead. The suppressions
  check still fails on an expired entry and on an inline suppression no entry
  covers.
- `.github/settings.yml` requires `gate / all gates passed` and
  `verify / gate` on the default branch. A `frontend-only` repository has no
  Go module and no caller of the shared bar, so it requires `verify / gate`
  alone.
- The verify and release pipelines require `.template.yaml` version `v1.1.0`
  or later, and name `.github/workflows/ci.yml` as the file they rely on.
- The pre-commit hook and `make check` delegate to lateregate v0.50.1, pinned
  in `go.mod`. `.lateregate.yaml` declares the identity role, the Postgres
  role, and the license, restates no shared default, and exempts the store
  and the migrate command from the unit coverage floor, which the
  integration tier measures instead.
- `make example` leaves out the files the shared bar writes on a run in the
  reference service.

## v1.0.0 - 2026-09-24

The first release. A service starts from it with
`go run latere.ai/x/service-template/cmd/template@latest init`, which needs no
checkout of this repository, and its pipeline callers resolve `@v1`. A
service scaffolded from a checkout before this release moves onto it with
`go run latere.ai/x/service-template/cmd/template@v1.0.0 upgrade`, and
removes any waiver it declared for `.env.example`.

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
- The template's own gate scaffolds a service from every change and runs the
  service's checks, the drift check among them, so the path the README
  documents is proven on each change rather than on adoption.

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
  the service owns.
- `template sync` and `template upgrade` keep a generated file the service
  edited while a live waiver covers it, instead of overwriting it, and print
  the template's change to the file beside the report. An expired waiver
  stops them before they write anything.
- The verify and release pipelines accept `.template.yaml` versions from
  v1.0.0; no earlier version names a release the drift check can run.
- The skeleton serves its probes through `latere.ai/x/pkg/health` (pkg
  v0.58.0): `/livez` and `/readyz` answer `ok` as text, `/readyz` names
  each failing dependency as `not ready: <check>: <error>`, and `/version`
  reports `version`, `commit`, and `build_time`. `/healthz` answers as
  `/livez` for one release and is removed in the next. The smoke tool reads
  the new bodies and pins the entry asset from the served document alone.

### Fixed

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
