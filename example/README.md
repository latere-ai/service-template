# service

An HTTP service in Go. It starts from configuration alone, serves JSON behind a
fixed middleware chain, reports its health and its build identity to an
orchestrator, and shuts down without dropping a request.

The repository ships the parts a service needs on its first day: a typed
configuration boundary, structured logs, traces and metrics, an error envelope
every route shares, a versioned interface, and a build that gates formatting,
lint, static analysis, tests, and coverage.

## Quick start

You need Go 1.27 or later, GNU Make, and a container engine that speaks compose,
such as Docker or Podman, for the dependency stack.

```sh
cp .env.example .env
make dev
```

`make dev` starts the dependencies, applies migrations, seeds a small data set,
and runs the service with live reload. It is idempotent, so running it against a
running stack converges instead of failing.

The service answers on the address in `ADDR`, `:8080` by default:

```sh
curl -s localhost:8080/livez
curl -s localhost:8080/readyz
curl -s localhost:8080/version
```

`make dev-down` stops everything and removes the volumes, so a broken local
state is one command away from clean. `make dev-seed` reloads the development
data set on its own.

The stack is namespaced by the directory name and its host ports are derived
from it, so two checkouts of different services run at the same time. Run
`make dev-ports` to see which ports this checkout binds.

## Build and test

```sh
make build
make test
make
```

`make build` writes a stamped binary to `out/`. `make test` runs the unit tier
with the race detector. A bare `make` runs the full local gate: formatting,
static analysis, lint, and tests.

## Documentation

| Document | Read it for |
| --- | --- |
| [Architecture](docs/architecture.md) | How the parts fit together |
| [Configuration](docs/configuration.md) | Every setting, its default, and its effect |
| [Interface reference](docs/api.md) | Endpoints, the error envelope, and the versioning rule |
| [Operations](docs/operations.md) | Probes, signals, common failures, and rollback |
| [Contributing](CONTRIBUTING.md) | Workflow, standards, and the commit format |
| [Security](SECURITY.md) | How to report a vulnerability |

The configuration reference and the interface reference are generated from the
code. Run `make docs` after changing a configuration field or a route.
