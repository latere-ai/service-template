# Adopting the template

For the developer who starts a service from this template and keeps it
current. It covers scaffolding, the files you change on the first day, wiring
the pipelines, and absorbing a template update. The rules behind each step are
in [the template contract](contract.md).

## Scaffold

Run the generator from a checkout of this repository:

```sh
go run ./cmd/template init \
  -C ../my-service \
  -module github.com/acme/my-service \
  -name my-service \
  -profile service \
  -features frontend,seo,database \
  -version v0.1.0
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-C` | `.` | the directory to scaffold into. It must not already hold a `.template.yaml` |
| `-module` | required | the Go module path of the new repository |
| `-name` | the last element of `-module` | the service name: lower case letters, digits, and hyphens. It names `cmd/<name>/` and the Kubernetes objects |
| `-profile` | `service` | `service`, `library`, or `frontend-only` |
| `-features` | none | a comma separated list of `frontend`, `seo`, `i18n`, `database`, `background` |
| `-version` | the generator's own release | the template version to record. A build from a checkout has none, so pass it |
| `-template` | `github.com/latere-ai/service-template` | the template identity to record, for a fork |
| `-skeleton` | found by walking up from the working directory | the skeleton tree to generate from. `TEMPLATE_SKELETON` sets it too |

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
`docs/architecture.md`, `docs/operations.md`, `specs/`, `migrations/`,
everything under `deploy/`, `.github/CODEOWNERS`, `.github/suppressions.yml`,
`tools/smoke/checks.yaml`, and the frontend's routes and `package.json`.

Generated files are the machinery every service shares: `.lateregate.yaml`,
`.githooks/`, `.github/workflows/verify.yml` and `settings.yml`,
`.github/settings.yml`, `.github/dependabot.yml`, the two Dockerfiles,
`docker-compose.yml`, `.env.example`, the `make/` fragments, and the rest of
`tools/`. Add your own dependency services in `compose.override.yml`, which
the container engine merges and the template never writes.

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

The pipelines are reusable workflows in this repository. A service commits a
thin caller for each one. Until `v1` is tagged, replace `@v1` in each caller
with a full commit SHA of this repository.

| Workflow | Caller | What it needs from you |
| --- | --- | --- |
| `verify.yml` | generated at `.github/workflows/verify.yml` | nothing. The job reports as `verify / gate`, the check branch protection requires |
| `settings.yml` | generated at `.github/workflows/settings.yml` | a repository secret `SETTINGS_TOKEN` with administration rights on the repository. The weekly run reports drift; a dispatched run with `mode: apply` writes the settings |
| `release.yml` | copy [`examples/release.yml`](../examples/release.yml) to `.github/workflows/release.yml` | `production-url`, `cluster-api-url`, and `cluster-ca` (the example reads it from the repository variable `CLUSTER_CA`); `preproduction-url` to smoke pre-release tags |
| `deps.yml` | none shipped; write one that calls it with `job` set to `pins`, `automerge`, or `template-version` | `pins` checks dependency pinning on every change, `automerge` merges a dependency update once the gate passes, and a scheduled `template-version` keeps one open issue while a newer template release exists |

A release is a `v*` tag. The pipeline gates the tag on a passing verify run
on the default branch, proves its credentials, builds and attests the image,
deploys the production overlay (the pre-production overlay for a pre-release
tag), smokes the live service, rolls back if the smoke fails, and publishes
the GitHub release last. The cluster credential is a short-lived token
exchanged for the workflow's own identity; nothing is stored in the
repository.

## Keep it current

Run the `template` command from a checkout of this repository at the version
your `.template.yaml` names. It compares against the skeleton tree it reads,
so a checkout at another version compares against that version instead.

```sh
cd service-template
git checkout v0.1.0      # the version the service declares, once it is tagged
go run ./cmd/template check -C ../my-service
```

| Command | What it does |
| --- | --- |
| `check` | compares every generated and merged file against the template and the lock. Changes nothing |
| `sync` | rewrites generated files and the managed regions to the declared version |
| `upgrade -version vX.Y.Z` | records the new version in `.template.yaml`, syncs, and prints the diff of every file it changed |

`check` exits with a code that names the remedy:

| Exit | Meaning | What to do |
| --- | --- | --- |
| 0 | clean | nothing |
| 3 | the repository edited a generated file | revert the edit, send it upstream as a change to the template, or declare a waiver |
| 4 | the repository is behind the template | run `upgrade` and review the diff |
| 1 | the check could not run: a malformed declaration, a merged file with missing markers, or an expired waiver | fix what the message names |

The verify and release workflows also check the declared version. Each names
the lowest `.template.yaml` version it works with, and a service below it fails
the first job with an instruction to upgrade.

### Waivers

A waiver records a generated file you diverge on deliberately. It turns the
`edited` verdict for that path into `waived` until it expires, and an expired
waiver fails the check.

```yaml
waivers:
  - path: .env.example
    reason: the service adds settings of its own and regenerates this file
    expires: 2027-03-31
```

A waiver changes the verdict of `check` and nothing else: `sync` and
`upgrade` still rewrite a waived file to the template's copy, so reapply your
version afterwards.

`.env.example` is the common case today. The template generates it, and the
service regenerates it from its own configuration struct with
`make env-example`, so a service that adds a setting either declares this
waiver or fails the check with exit 3. After a `sync`, run `make env-example`
again, or the service's own staleness check fails.

## Known limitations

- **The drift check in CI.** A new service's `make template-check` target,
  which the verify pipeline runs, tells you to install the command with
  `go install github.com/latere-ai/service-template/cmd/template@v1`. That
  does not resolve: the module path is `latere.ai/x/service-template`, there
  is no `v1` tag yet, and the command needs a skeleton tree beside it. Run the
  check from a checkout as shown above.
- **Profile documents.** The `library` and `frontend-only` profiles receive
  the same seed `README.md` and `CONTRIBUTING.md` as the `service` profile,
  which describe an HTTP service and targets such as `make dev` those profiles
  do not have. Rewrite them as part of the first changes.
