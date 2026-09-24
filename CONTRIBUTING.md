# Contributing

This file is for people changing the template itself. A developer building a
service from it reads the [README](README.md) and
[`docs/adopting.md`](docs/adopting.md); the files a new service receives carry
their own `CONTRIBUTING.md`.

## How the repository fits together

Three things have to stay in step: the generator, the skeleton it ships, and
the reference service the two produce together.

```
cmd/template/             the generator and drift check
internal/generator/       manifest loading, rendering, planning, drift verdicts
internal/skeleton/        the skeleton packed into the archive the command embeds
internal/verifypipeline/  tests for the pipeline scripts and the workflow structure
skeleton/                 the files a service receives, as a compiling Go module
  cmd/service/              the entry point, plus one wiring file per feature
  internal/                 config, observability, httpx, server, auth, store,
                            worker, web, version, testsupport
  frontend/                 Bun, React, TypeScript, Vite, Vitest, prerender, i18n
  tools/                    assethash, docgen, release, settings, smoke
  deploy/                   kustomize base and overlays
  manifests/                one manifest fragment per content group
  make/                     the make fragments the skeleton Makefile includes
.github/workflows/        the reusable pipelines services call, and template.yml,
                          this repository's own gate
.github/scripts/          the scripts those pipelines run
examples/                 the caller workflows a service commits
example/                  a reference service generated with every feature on
docs/                     the adoption guide and the template contract
specs/                    design records, one aspect per spec
```

The skeleton is its own Go module under the path `example.com/service`, so
the shipped code compiles and its tests run here, before any service receives
it. Generation substitutes two anchored literals, the module path and the name
`service`, renames `cmd/service/` to `cmd/<name>/`, and renders every file
whose skeleton name ends in `.tmpl` with Go's `text/template`. A `.tmpl` file
sees `.Template`, `.Version`, `.Module`, `.Name`, `.Profile`, and
`.Features`. Anything else that needs the service's name belongs in a `.tmpl`
file: a bare substitution of `service` would also hit `services` and
`http.Server`.

`example/` is generated output that is committed. `make example` regenerates
it and fails on any difference, so it doubles as the determinism check and as
a readable sample of what a service receives.

The command carries the skeleton inside its binary, so a release run as
`go run latere.ai/x/service-template/cmd/template@<version>` needs no
checkout. `skeleton/` is a module of its own, which neither an embed pattern
nor a module download reaches into, so the command embeds
`internal/skeleton/skeleton.zip`: the manifest fragments and every file they
declare, stored uncompressed. It is generated output that is committed, like
`example/`, and the suite fails when it no longer matches the tree. The
command generates from that archive unless `-skeleton` or `TEMPLATE_SKELETON`
names a tree on disk, which is how the targets below reach the working copy.

## Changing the skeleton

1. Edit or add the file under `skeleton/`.
2. Declare a new file in exactly one fragment under `skeleton/manifests/`,
   with its `mode` (`seed`, `generated`, or `merged`) and, where it applies,
   the `profiles` and `features` that select it. An undeclared file fails
   `make manifest`, because a file the generator silently drops is how a fix
   stops reaching services.
3. Run `make validate`: it compiles and tests the skeleton under the race
   detector, lints it as its own module, proves the manifest complete,
   compares `example/` against a fresh generation, and scaffolds a service
   from the tree and runs its checks.
4. Run `make example-update` and commit the regenerated `example/` and
   `internal/skeleton/skeleton.zip` in the same change.

Choose the mode by who owns the content after the first day. The
[template contract](docs/contract.md) states the three modes, and every
fragment's comments say why its files are in the mode they are in. A change
to a generated file reaches every service on its next `template sync`, so it
is a release note; a change to a seed file reaches only services scaffolded
afterwards.

A reusable workflow that starts depending on a new or changed generated file
raises the minimum template version it accepts, `MIN_TEMPLATE_VERSION` in
`verify.yml` and `minimum-template-version` in `release.yml`, and that release
is at least a minor one.

## The build

CI is `template.yml`, with two jobs. `verify` runs the shared gates of
`latere-ai/ci` against the root module: formatting, lint, modernization, the
suite with and without the race detector, the hermetic and empty `TMPDIR`
suites, vulnerabilities, licenses, and the spec tree. `validate` runs
`make validate`: the skeleton compiled, tested, and linted as its own module,
the manifest check, the comparison of `example/` against a fresh generation,
and the adoption proof. Run `make all` before you push; it covers both.

The adoption proof, `make adoption`, is the README's path run end to end from
outside this checkout. It builds the command from this tree, scaffolds a
service into an empty temporary directory, and runs the service's own checks
there: build, vet, the suite, formatting, modernization, outbound tracing,
the environment reference, settings, the spec tree, and the drift check. It
then adds a setting, regenerates `.env.example`, and runs the checks that
must still pass. Lint and the frontend targets are left out, because they
need golangci-lint and Bun installed; `make validate` lints and tests the
same code as the skeleton module.

| Target | What it does |
| --- | --- |
| `make` / `make all` | formatting, modernization, build, the suite, the hermetic suite for both modules, the spec trees, `validate`, and lint |
| `make validate` | `skeleton-test`, `skeleton-lint`, `lint-otel`, `manifest`, `example`, and `adoption` |
| `make skeleton-test` | the skeleton built and tested as source with the race detector |
| `make skeleton-cover` | the skeleton's coverage gate over the unit and integration tiers. It needs a reachable Postgres and a browser for the diagram renderer, so it is not part of `all` |
| `make manifest` | every skeleton file declared in exactly one fragment |
| `make example` | the committed `example/` compared against a fresh generation |
| `make example-update` | regenerates `example/` and, first, the skeleton archive |
| `make adoption` | scaffolds a service from this tree into a temporary directory and runs its checks, the drift check among them |
| `make skeleton-archive` | packs `skeleton/` into the archive the command embeds |
| `make check` | the shared bar alone, `go tool lateregate`; `go tool lateregate list` names each gate |

## Workflow

Work here is spec-driven. Before you change behavior, there is a spec in
[`specs/`](specs/README.md) that describes the problem, the design, and the
acceptance criteria. Read it, then implement it.

1. Pick a spec whose `status` is `drafted` and whose `depends_on` entries are
   complete.
2. Implement the acceptance criteria. Add tests in the same change.
3. Update the spec with an Outcome section and set `status: complete`.

For a change with no spec, open an issue first and say which aspect of the
template it touches. A change that adds a new aspect needs a new spec, and a
spec is a reasonable first contribution on its own.

## Standards

The template holds itself to the standards it ships:

- Run `make all` before you push. It covers what CI runs.
- Never edit a file under `example/` by hand, a sweep included: change the
  skeleton and run `make example-update`, or `template.lock` falls behind the
  files it describes. The same holds for the skeleton archive.
- A bug fix carries a test that fails without the fix. That test is how the
  fix stays fixed.
- Coverage has a per-package floor, set in `.lateregate.yaml`. An exemption
  there carries the reason it exists.
- A change to translated text in the skeleton's frontend needs every locale,
  because the completeness gate fails on a locale left behind.

If you hit a genuine exception to any of these, say so in the pull request
rather than working around the check.

## Releasing

Every tag has a section in [`CHANGELOG.md`](CHANGELOG.md), and that section is
the body of the GitHub release. Write under `Unreleased` as work lands, for
the developer who maintains a service: what changed in the files they receive
and what they have to do. `go tool lateregate release vX.Y.Z` refuses while
CI is red, then turns `Unreleased` into the tag's section, commits, tags, and
pushes. The tag starts `template-release.yml`, which publishes the GitHub
release with that section as its body and then moves `v1` to the tag, because
the callers a service commits pin `@v1`. A pre-release tag publishes a
pre-release and leaves `v1` alone. The compatibility rules in the
[template contract](docs/contract.md) decide whether a change is a patch, a
minor, or a major release.

## Commit messages

`scope: lowercase description`. One logical change per commit.

## Writing

Every sentence a service built from the template emits or carries is written
for one reader, and the register follows the reader:

- User, a person or a coding harness: API error `message`, the frontend copy
  in every locale, the docs. Short and plain: what happened and what to do
  next, naming a command or a page, never a package, a function, a table, or
  a Kubernetes object.
- Contributor, someone changing a service built from the template: specs,
  contributing guides, package documentation, commit messages, source
  comments. Precise, in the project's own terms, with the reason a design is
  what it is.
- Developer, someone debugging a running system: logs, traces, startup
  failures. Exact and complete: object, operation, observed value, expected
  value, and the underlying error.

An error has one code, one fixed user sentence in `message`, and one
developer detail in a separate field shown only on request. The canonical
statement, worked examples, and the review checklist are in the
[registers document](https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md)
in pkg. The rule applies to new text and to reviews; existing text is fixed as
it is touched.

## Reporting problems

Use GitHub issues for defects and proposals. Use the process in
[SECURITY.md](SECURITY.md) for anything with a security impact.
