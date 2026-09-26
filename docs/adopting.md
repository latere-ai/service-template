# Adopting the template

For the developer who starts a service from this template and keeps it
current. It covers scaffolding, the files you change on the first day, wiring
the pipelines, and absorbing a template update. The rules behind each step are
in [the template contract](contract.md).

## Scaffold

Run the newest release of the generator. `go run` fetches it through the
module proxy, and it carries the skeleton of its release, so there is nothing
to clone or install:

```sh
go run latere.ai/x/service-template/cmd/template@latest init \
  -C my-service \
  -module github.com/acme/my-service \
  -name my-service \
  -profile service \
  -features frontend,seo,database
```

`.template.yaml` records the release that ran. Every later command against the
service runs that release, or the one it upgrades to, the same way.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-C` | `.` | the directory to scaffold into. It must not already hold a `.template.yaml` |
| `-module` | required | the Go module path of the new repository |
| `-name` | the last element of `-module` | the service name: lower case letters, digits, and hyphens. It names `cmd/<name>/` and the Kubernetes objects |
| `-profile` | `service` | `service`, `library`, or `frontend-only` |
| `-features` | none | a comma separated list of `frontend`, `seo`, `i18n`, `database`, `background` |
| `-version` | the generator's own release | the template version to record. A release records itself, and refuses any other value, because the files it writes are that release's |
| `-template` | `github.com/latere-ai/service-template` | the template identity to record, for a fork |
| `-skeleton` | the skeleton the command carries | a skeleton tree on disk to generate from instead, for a change to the template or a fork. `TEMPLATE_SKELETON` sets it too |

To scaffold from a checkout of this repository at a commit that is not
released yet, build the command with version control stamping, such as
`go run -buildvcs=true ./cmd/template init ...` or `go build ./cmd/template`.
A clean checkout at a pushed commit records the commit's pseudo-version, which
the module proxy resolves like a release, so the service's drift check can run
it later. A plain `go run ./cmd/template` stamps no version, and a checkout
with uncommitted changes has none that a download can reproduce; `init`
refuses both and says why. The pipelines accept a declared version from
`v1.0.0` on, so a pseudo-version of a commit before that release fails them.

The [README](../README.md#profiles-and-features) describes the profiles and the
features. A profile cannot be changed later; a feature can be switched on by
editing `features` in `.template.yaml` and running `template sync`.

`init` refuses a directory that already holds a declaration, because a second
run would overwrite files you own.

## What you own and what the template owns

Every file the template writes has one of three modes. The mode decides
whether you may edit it.

| Mode | Who owns it | What the template does with it |
| --- | --- | --- |
| seed | you | writes it once at `init`, then never rewrites or checks it |
| generated | the template | rewrites it on `sync`; any local change is drift |
| merged | shared | rewrites only the region between the managed markers; the rest is yours |

Seed files are the service itself and its description: `cmd/<name>/`, the
feature wiring, `go.mod`, `README.md`, `CONTRIBUTING.md`, `SECURITY.md`,
`docs/`, `specs/`, `migrations/`, everything under `deploy/`,
`.github/CODEOWNERS`, `.github/suppressions.yml`, `tools/smoke/checks.yaml`,
the configuration struct in `internal/config/config.go`, `.env.example`, and
the frontend's routes and `package.json`. `.env.example`,
`docs/configuration.md`, and `docs/api.md` are derived from your code by
`make env-example` and `make docs`, so they describe your service and are
yours; their check targets, not the drift check, keep them current.

Generated files are the machinery every service shares: `.lateregate.yaml`,
`.githooks/`, the callers `.github/workflows/ci.yml`, `verify.yml`,
`ci-gate-bump.yml`, and `settings.yml`,
`.github/settings.yml`, `.github/dependabot.yml`, the two Dockerfiles,
`docker-compose.yml`, the configuration loader, the `make/` fragments, and the
rest of `tools/`. Add your own dependency services in `compose.override.yml`,
which the container engine merges and the template never writes.

Merged files are `Makefile`, `.gitignore`, and `frontend/.gitignore`. Add your
own targets and ignores outside these lines, and leave what is between them
alone:

```
# >>> template: managed region, do not edit <<<
# >>> template: end managed region <<<
```

`template.lock` records a digest of every file the template wrote. The
generator maintains it; never edit it by hand.

## The first changes

The seed files describe a generic service called `service`. Make them yours
before the first push:

1. **The documents.** `README.md`, `docs/architecture.md`,
   `docs/operations.md`, and `specs/` describe the skeleton. Rewrite them for
   your service. `docs/configuration.md` and `docs/api.md` are derived from
   the code by `make docs`; regenerate them rather than editing them.
2. **Ownership.** `.github/CODEOWNERS` names the owners required reviews go
   to.
3. **The deployment.** `deploy/base/` holds the Deployment, Service, service
   account, and disruption budget. `deploy/production/` and
   `deploy/preproduction/` are the overlays a release tag and a pre-release tag
   deploy. Set the hostnames, replicas, and resources there.
4. **The pipeline's credentials.** `deploy/pipeline-secrets.yaml` declares
   every credential the release pipeline uses. Replace `OWNER/REPO` in each
   `subject` with your repository, and configure the matching trust policy on
   the receiving side.
5. **The cluster's one-time resources.** Apply what `deploy/bootstrap/` holds
   by hand, as its README describes. The pipeline never applies that
   directory.
6. **What "live" means.** `tools/smoke/checks.yaml` lists the endpoints the
   release smoke run asserts against the live address, beside the readiness
   and build identity checks the template always runs. Add the routes whose
   failure would mean the release is not serving.

## Wire the pipelines

The pipelines are reusable workflows. Two run on every push. `ci.yml` calls
the fleet's shared per-push bar in `latere-ai/ci`, which asks lateregate,
pinned in `go.mod`, which gates apply and runs one job per gate: formatting,
lint, `go vet`, the vulnerability scan, the license notice, the spec tree,
and the suite with its race, hermetic, temporary-directory, and coverage
properties. `verify.yml` in this repository runs what that bar does not
cover: the drift check, the declared settings and ownership, tracked
suppressions, CodeQL, the integration tier against Postgres, the frontend,
and the build the release consumes. No check runs in both.

A service commits a thin caller for each one, pinned to `@v1`, the moving tag
that follows the newest `v1` release. Pin a full version tag such as
`@v1.1.0` instead to take workflow changes only when you move the pin.

| Workflow | Caller | What it needs from you |
| --- | --- | --- |
| `latere-ai/ci` `lateregate.yml` | generated at `.github/workflows/ci.yml` | nothing. The aggregate reports as `gate / all gates passed`, a check branch protection requires |
| `verify.yml` | generated at `.github/workflows/verify.yml` | nothing. The job reports as `verify / gate`, the other check branch protection requires |
| `latere-ai/ci` `ci-gate-bump.yml` | generated at `.github/workflows/ci-gate-bump.yml` | nothing. Once a day it moves the `latere.ai/x/ci-gate` pin to the latest release when the whole bar passes on it and dispatches `ci.yml` on that commit, or opens one issue for the version when the bar fails |
| `settings.yml` | generated at `.github/workflows/settings.yml` | a repository secret `SETTINGS_TOKEN` with administration rights on the repository. The weekly run reports drift; a dispatched run with `mode: apply` writes the settings |
| `release.yml` | copy [`examples/release.yml`](../examples/release.yml) to `.github/workflows/release.yml` | `production-url`, `cluster-api-url`, and `cluster-ca` (the example reads it from the repository variable `CLUSTER_CA`); `preproduction-url` to smoke pre-release tags |
| `deps.yml` | none shipped; write one that calls it with `job` set to `pins`, `automerge`, or `template-version` | `pins` checks dependency pinning on every change, `automerge` merges a dependency update once the gate passes, and a scheduled `template-version` keeps one open issue while a newer template release exists |

Both aggregates are required status checks. `.github/settings.yml` declares
`gate / all gates passed` and `verify / gate` as the contexts on the default
branch, and the settings workflow applies them: dispatch it with
`mode: apply`, or run `make settings-apply` with an administrative token.
`make settings-required-check` reports a context the branch does not require yet.
Each context is `<caller job id> / <job name>`, so renaming the `gate` job in
`ci.yml` or the `verify` job in `verify.yml` unbinds the rule.

A release is a `v*` tag. The pipeline gates the tag on a passing verify run
on the default branch, and the shared bar's result reaches it through branch
protection, which refuses a merge the bar failed. It then proves its credentials, builds and attests the image,
deploys the production overlay (the pre-production overlay for a pre-release
tag), smokes the live service, rolls back if the smoke fails, and publishes
the GitHub release last. The cluster credential is a short-lived token
exchanged for the workflow's own identity; nothing is stored in the
repository.

## Keep it current

Every command runs as a template release, fetched by `go run`, from the
service's directory. Nothing is installed and no checkout of this repository
is needed:

```sh
make template-check                                              # check against the declared release
go run latere.ai/x/service-template/cmd/template@v1.0.0 sync     # the release .template.yaml declares
go run latere.ai/x/service-template/cmd/template@v1.1.0 upgrade  # the release to move to
```

`make template-check` reads the version from `.template.yaml` and runs `check`
at it; the verify pipeline runs the same target. To check with another build
of the command, name it: `make template-check TEMPLATE_COMMAND=/path/to/template`.

A release carries its own skeleton and compares against nothing else, so it
refuses to sync or check a repository that declares a different release, and
to record a different version, and prints the `go run` command for the release
that can.

| Command | What it does |
| --- | --- |
| `check` | compares every generated and merged file against the template and the lock. Changes nothing |
| `sync` | rewrites generated files and the managed regions to the declared version |
| `upgrade` | records the release that runs in `.template.yaml`, syncs, and prints the diff of every file it changed |

`check` exits with a code that names the remedy:

| Exit | Meaning | What to do |
| --- | --- | --- |
| 0 | clean | nothing |
| 3 | the repository edited a generated file | revert the edit, send it upstream as a change to the template, or declare a waiver |
| 4 | the repository is behind the template | run `upgrade` and review the diff |
| 1 | the check could not run: a malformed declaration, a merged file with missing markers, or an expired waiver | fix what the message names |

The verify and release workflows also check the declared version. Each names
the lowest `.template.yaml` version it works with, `v1.1.0` today, and a
service below it fails the first job with the `upgrade` command to run.

### Waivers

A waiver records a generated file you diverge on deliberately. It turns the
`edited` verdict for that path into `waived` until it expires.

```yaml
waivers:
  - path: .lateregate.yaml
    reason: the coverage floor waits for the storage tests
    expires: 2027-03-31
```

`sync` and `upgrade` keep a waived file you edited as it is. They list it
under `kept under a waiver`, and `upgrade` prints the template's change to the
file beside the report, so you can fold the change into your copy. A waiver
protects an edit only: a waived file you have not edited is updated like any
other.

When a waiver expires, `check` fails and `sync` and `upgrade` stop before
they write anything. Renew the expiry to keep the edit, or remove the waiver
and run `sync` to take the template's copy. A waiver on a file you own, such
as one declared for `.env.example` before it became a seed file, does nothing;
`check` warns about it, and you remove it.

## Known limitations

- **Profile documents.** The `library` and `frontend-only` profiles receive
  the same seed `README.md` and `CONTRIBUTING.md` as the `service` profile,
  which describe an HTTP service and targets such as `make dev` those profiles
  do not have. Rewrite them as part of the first changes.
