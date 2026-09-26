# service-template

[![Template](https://github.com/latere-ai/service-template/actions/workflows/template.yml/badge.svg)](https://github.com/latere-ai/service-template/actions/workflows/template.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**A template for a production Go service with an optional Bun and React
frontend.** It gives a new service what every service needs and nobody wants
to write twice: typed configuration, observability, probes and graceful
shutdown, a lint and coverage gate, a container image, a tag-driven release
that deploys and smokes before it publishes, repository settings as code, and a
spec-driven workflow.

It is not a starter kit you copy once and forget. The files it generates stay
owned by the template, a drift check proves a service still matches the
version it declares, and a fix made here reaches every service through one
command.

## Start a service

You need Go 1.27 or newer, git, and GNU Make. A service with a frontend also
needs [Bun](https://bun.sh), and `make dev` needs a container engine that
speaks compose, such as Docker or Podman.

```sh
go run latere.ai/x/service-template/cmd/template@latest init \
  -C my-service \
  -module github.com/acme/my-service \
  -name my-service \
  -profile service \
  -features frontend,database
```

There is nothing to clone or install: `go run` fetches the `template` command
at the newest release, and the command carries the skeleton of that release.
`init` writes `.template.yaml`, every file the profile and the selected
features declare, and `template.lock`. `.template.yaml` records the release
that ran, and every later check of the service runs that same release.

To scaffold from a checkout of this repository instead, at a commit that is
not released yet, build the command with version control stamping:
`go run -buildvcs=true ./cmd/template init ...`. A clean checkout at a pushed
commit records that commit's pseudo-version, which the module proxy resolves
like a release. A plain `go run ./cmd/template` stamps no version, and a
checkout with uncommitted changes has none that a download can reproduce, so
`init` refuses both and says so.

Then, in the new repository:

```sh
cd my-service
git init
cp .env.example .env
make dev    # dependencies, migrations, seed data, and the service with live reload
make        # the full local gate
```

The new repository's own `README.md` and `CONTRIBUTING.md` take it from there.
[`docs/adopting.md`](docs/adopting.md) lists what to change first, how to wire
the pipelines, and which files you own.

## Profiles and features

A profile is the shape of the repository and is fixed at scaffold time.
Features are switched on with `-features` and are independent of each other.

| Profile | What it scaffolds | Features it accepts |
| --- | --- | --- |
| `service` | an HTTP service in Go, with deploy manifests and the release pipeline | all five |
| `library` | a Go module of importable packages, with the quality gate | none |
| `frontend-only` | a Bun and React application with no Go backend | `frontend` (always on), `seo`, `i18n` |

| Feature | What it adds |
| --- | --- |
| `frontend` | Bun, React, TypeScript, Vite, and Vitest, served by the Go binary |
| `seo` | a build-time prerender that emits crawlable HTML, a sitemap, and structured data; needs `frontend` |
| `i18n` | message catalogs, locale negotiation, and a completeness gate; needs `frontend` |
| `database` | Postgres through pgx, migrations, and a migration command |
| `background` | scheduled jobs, queue consumers, and one-shot commands that share the service lifecycle |

A scaffold with no feature selected still builds: the entry point reaches the
store, the frontend, and the job runner through seams that exist only when the
feature that owns them was selected.

## What a new service gets

```mermaid
flowchart LR
  subgraph Repo["Your service repository"]
    SK["cmd/ internal/ frontend/"]
    CFG[".lateregate.yaml<br/>.githooks/<br/>Makefile"]
    CALL[".github/workflows<br/>thin callers"]
  end
  subgraph T["service-template"]
    WF["reusable workflows<br/>verify, release, settings, deps"]
    GEN["template command<br/>init, sync, check, upgrade"]
  end
  LIB["latere.ai/x/pkg<br/>latere.ai/x/ci-gate"]
  BAR["latere-ai/ci<br/>shared per-push bar"]
  CALL -->|uses| WF
  CALL -->|uses| BAR
  BAR -->|runs the gates of| LIB
  CFG -->|generated and checked by| GEN
  SK -->|imports| LIB
  WF -->|publishes| REL["release with<br/>smoke evidence"]
```

- **Backend.** Go 1.27, a fixed module layout, typed configuration read from
  the environment, graceful shutdown, `/livez`, `/readyz`, and a `/version`
  endpoint that reports the version, commit, and build time, and OpenTelemetry
  traces, metrics, and trace-correlated logs.
- **Quality gates.** The shared gates of `latere.ai/x/ci-gate`, pinned in
  `go.mod` and configured in one `.lateregate.yaml`: formatting,
  modernization, the shared `golangci-lint` set, `go vet`, `govulncheck`,
  the license notice, the suite under the race detector, with only the
  toolchain on `PATH`, and against an empty `TMPDIR`, per-package coverage,
  outbound tracing, and the spec tree. `.github/workflows/ci.yml` runs them
  on every push through the fleet's shared pipeline, and `make check` runs
  the same set on a workstation. The template's verify pipeline adds what
  that bar does not cover: the drift check, the declared settings, tracked
  suppressions, CodeQL, the integration tier against Postgres, and the
  frontend. No check runs in both.
- **Delivery.** A multi-stage container image with a software bill of
  materials, build provenance, and a keyless signature. A version tag starts
  one pipeline that gates the tag, builds, deploys, smokes the live service,
  rolls back when the smoke fails, and publishes the release last, with the
  smoke output attached as evidence.
- **Repository settings as code.** Branch protection, required checks,
  ownership, and merge rules are declared in the repository and applied by a
  workflow, so the gates are binding rather than advisory.
- **Documentation that stays current.** The configuration reference and the
  API reference are generated from the code, and a check target fails when a
  committed copy is stale, a link does not resolve, or a diagram does not
  render.
- **A spec workflow.** A spec directory with a lifecycle for each spec, and a
  validator that holds the index to the files.

## Keeping a service current

The template reaches a service through three layers, and each has its own
update path:

| Layer | What it is | How a service stays current |
| --- | --- | --- |
| Workflows | the reusable `workflow_call` pipelines in this repository | the service's thin callers pin a tag; a fix lands here |
| Generated files | `.lateregate.yaml`, git hooks, `make/` fragments, the Dockerfiles, the callers, and the rest of what the template owns | `template sync` rewrites them; `template check` fails on drift |
| Libraries | the Go packages the skeleton imports from `latere.ai/x/pkg`, and the gate in `latere.ai/x/ci-gate` | ordinary dependency updates |

Each command runs as a release, fetched by `go run`, from the service's own
directory:

```sh
make template-check                                              # compare against the declared release; changes nothing
go run latere.ai/x/service-template/cmd/template@v1.0.0 sync     # rewrite generated files to the declared release
go run latere.ai/x/service-template/cmd/template@v1.1.0 upgrade  # move to v1.1.0, sync, and print the diff
```

`make template-check` runs `check` at the release `.template.yaml` declares,
and the verify pipeline runs the same target. `check` exits 0 when the
repository is clean, 3 when it edited a generated file, 4 when it is behind
the template, and 1 when the check could not run. The command carries the
skeleton of its own release, so it refuses to sync or check a repository that
declares a different one, and names the release to run instead.

Releases are tagged `vX.Y.Z`, and the moving `v1` tag the pipeline callers pin
follows the newest `v1` release. [`CHANGELOG.md`](CHANGELOG.md) says what each
release changed in the files a service receives.

## Design principles

**Central logic, local variability.** A reusable workflow owns ordering and
orchestration. A service declares what to build and what "live" means through
standard directories and scripts, not a long list of workflow inputs.

**Drift is detected, not hoped against.** Every file the template generates is
regenerable, and the check proves the copy in the repository still matches the
version it claims.

**Every gate fails loudly.** A skipped test, an unreachable database, or a
missing secret fails the job. A gate that passes when it cannot run is not a
gate.

**No provider lock-in in the core.** Authentication, storage, and telemetry
backends sit behind interfaces the template defines and does not implement for
one vendor.

## Documentation

| | |
| --- | --- |
| [Adopting the template](docs/adopting.md) | scaffolding, the first changes, wiring the pipelines, and keeping a service current |
| [`CHANGELOG.md`](CHANGELOG.md) | what each release changed for a service |
| [The template contract](docs/contract.md) | what the template owns and what a service owns, file modes, drift verdicts, and the compatibility promise |
| [`examples/`](examples) | the caller workflows a service commits |
| [`example/`](example) | a reference service generated with every feature on; `make example` proves it matches a fresh generation |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | changing the template itself |
| [`specs/`](specs/README.md) | the design records behind each part |

## Contributing

Issues and pull requests are welcome. [`CONTRIBUTING.md`](CONTRIBUTING.md)
covers how this repository fits together, the build, and how a change to the
skeleton reaches services. Report a vulnerability through
[`SECURITY.md`](SECURITY.md), not in a public issue.

## License

MIT. See [`LICENSE`](LICENSE).
