# Contributing

## Before you write code

Work that changes behaviour starts with a spec in `specs/`. A spec states the
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
make dev        # dependencies, migrations, seed data, live reload
make            # the full local gate
```

A bare `make` runs what the pipeline runs: formatting, static analysis, lint,
and tests. Run it before you push. The pipeline runs the same targets, so a
green local run is evidence rather than a hope.

| Target | What it does |
| --- | --- |
| `make build` | Build the stamped binary into `out/` |
| `make test` | Unit tier with the race detector |
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

Coverage is gated. The floor every package has to clear is in `.lateregate.yaml`,
and the gate fails when a package is below it or when a package produced no
coverage data at all.

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

## Generated files

Some files in this repository are generated. The header of each one says so.
Edit the source, not the output:

| Output | Source | Command |
| --- | --- | --- |
| `.env.example` | The configuration struct | `make env-example` |
| `docs/configuration.md` | The configuration struct | `make docs` |
| `docs/api.md` | The route table and the error envelope | `make docs` |

A check target proves each committed copy is current, and the check runs in the
pipeline. An edit to a generated file is reverted by the next regeneration.

`specs/README.md` is written by hand. `make spec-check` proves every row agrees
with the spec it links to, so a status the table claims and the file denies
fails the build instead of misleading the next reader.

## Review

A reviewer checks the acceptance criteria, the tests, and the failure paths. A
pull request that changes behaviour with no test, or that leaves a generated
document behind, is not ready.
