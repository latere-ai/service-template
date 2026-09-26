# Contributing

## Before you write code

Work that changes behavior starts with a spec in `specs/`. A spec states the
problem before the solution, lists acceptance criteria a reviewer can check
without interpretation, and records what shipped when the work is done. A design
decision that lives only in a pull request description is unfindable six months
later, and the next reader changes the code back.

Read `specs/README.md` for the current queue. It is generated from the spec
files, so it cannot disagree with them.

A change that fixes a defect needs no spec. It needs a test that fails without
the fix.

## The loop

```sh
make hooks      # once per clone: the pre-commit and pre-push hooks
make dev        # dependencies, migrations, seed data, live reload
make check      # the whole shared bar, as CI runs it
```

The gates live in `latere.ai/x/ci-gate`, pinned in `go.mod` and configured in
`.lateregate.yaml`. `make check` runs every one of them, `go tool lateregate`
under another name, and `.github/workflows/ci.yml` runs the same binary at the
same version on every push, so a green local run is evidence rather than a
hope. Run it before you push. The pre-commit hook checks the staged Go files
in seconds, and the pre-push hook lints the packages a push changes and
refuses a release tag with no changelog section. A bare `make` runs the fast
local subset.

`.github/workflows/verify.yml` runs beside it with what the shared bar does
not cover: the template drift check, the declared settings and ownership,
tracked suppressions, code scanning, the integration tier against Postgres,
and the frontend. A pull request merges when both report green, as
`gate / all gates passed` and `verify / gate`.

| Target | What it does |
| --- | --- |
| `make build` | Build the stamped binary into `out/` |
| `make check` | The whole shared bar |
| `make test` | `go vet` and the unit tier |
| `make test-race` | The unit tier with the race detector |
| `make test-integration` | Integration tier, which needs the dependency stack |
| `make cover` | Both tiers with the coverage gate |
| `make lint` | Lint the module |
| `make docs` | Regenerate the derived documents |
| `make docs-check` | Prove the committed documents match the code |
| `make spec-check` | Validate the spec directory and its index |
| `make dev-down` | Remove the local stack and its volumes |
| `make dev-seed` | Reload the development data set |
| `make dev-ports` | Print the ports this checkout binds |

## Standards

Every change carries its tests. A defect fix carries the test that reproduces
the defect: a fix with no failing test to prove it is a claim, not a fix.

Coverage is gated. Every package clears the shared floor, and the gate fails
when a package is below it or when a package produced no coverage data at all.
A package that cannot be measured without a dependency the suite does not have
is exempted in `.lateregate.yaml` with the reason, and `make cover` measures it
with the integration tier.

Handle every error. Return it with context, or log it. A discarded error is a
failure that reappears later without its cause.

Comments state what a thing is and why it exists. They do not record process,
status, or the history of the file, because that content is wrong within a month
and the version history already holds it.

Documents address one audience each. A reference is exhaustive and precise. A
guide explains value and use. Mixing the two produces a document nobody
finishes.

## Commits

One logical change per commit, with a message in the form
`scope: lowercase description`:

```
config: read the OTLP endpoint from the environment
httpx: reject a body over the size limit before reading it
```

The scope is the package or the area the change belongs to. The description says
what changed, in the imperative, without a trailing period.

## Files the template owns

This repository was scaffolded from
[service-template](https://github.com/latere-ai/service-template), and
`.template.yaml` names the template version it follows. The template keeps
owning part of what it wrote, and `template.lock` records a digest of each
file it wrote:

| Mode | Files | What you do |
| --- | --- | --- |
| seed | the service's own code, documents, specs, and deployment, written once when the repository was scaffolded | change them freely |
| generated | the shared machinery: `.lateregate.yaml`, `.githooks/`, the workflow callers, `.github/settings.yml`, the `make/` fragments, and most of `tools/` | leave them alone; `make template-check` reports a change as drift |
| merged | `Makefile` and `.gitignore` | edit outside the lines that mark the managed region |

A change a generated file needs belongs in the template, where it reaches
every service built from it, and arrives here with the next template upgrade.
To diverge on one deliberately, declare a waiver in `.template.yaml` with a
path, a reason, and an expiry date: the check reports the file as waived, and
a template sync or upgrade keeps your copy and prints the template's change to
it. `make template-check` runs the check at the template release
`.template.yaml` declares, as the verify pipeline does. The template's
[adoption guide](https://github.com/latere-ai/service-template/blob/main/docs/adopting.md)
describes `check`, `sync`, and `upgrade`, and how to run each at a release.

## Files derived from the code

Some files in this repository are generated from the code. The header of each
one says so. Edit the source, not the output:

| Output | Source | Command |
| --- | --- | --- |
| `.env.example` | The configuration struct | `make env-example` |
| `docs/configuration.md` | The configuration struct | `make docs` |
| `docs/api.md` | The route table and the error envelope | `make docs` |

A check target proves each committed copy is current, and the check runs in the
pipeline. An edit to a generated file is reverted by the next regeneration.

The outputs are this repository's own, like the code they are derived from.
The template wrote each one once, when it scaffolded the repository, and
neither checks nor rewrites them afterwards.

`specs/README.md` is written by hand. `make spec-check` proves every row agrees
with the spec it links to, so a status the table claims and the file denies
fails the build instead of misleading the next reader.

## Review

A reviewer checks the acceptance criteria, the tests, and the failure paths. A
pull request that changes behavior with no test, or that leaves a generated
document behind, is not ready.
