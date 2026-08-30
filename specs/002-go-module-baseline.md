---
title: "Go module baseline: layout, toolchain pin, build, and version stamping"
status: drafted
depends_on:
  - specs/001-template-contract.md
affects: [skeleton/go.mod, skeleton/cmd/, skeleton/internal/, skeleton/Makefile]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Go module baseline

## Problem

Service repositories agree on Go 1.27 and then diverge on everything else: where
the entry point lives, how the binary learns its own version, which build flags
CI uses, and what `make build` produces. A release pipeline cannot be generic
while the thing it builds is not.

## Scope

Layer 2. The Go module skeleton, its directory layout, its build target, and the
version information compiled into the binary.

## Design

### Layout

```
cmd/<service>/main.go     entry point, flag parsing, wiring only
internal/config/          typed configuration
internal/server/          HTTP server, lifecycle, probes
internal/handler/         request handlers
internal/store/           persistence
internal/version/         build metadata
```

`main.go` holds no business logic. It reads configuration, constructs the
server, and blocks on the lifecycle. Everything a test needs must be reachable
without running `main`.

### Toolchain pin

`go.mod` declares `go 1.27.0`. CI reads the version from `go.mod` and never from
a hardcoded string in a workflow, so an upgrade is a one-line change in one file.

### Version stamping

`internal/version` exposes:

```go
var (
    Version   string // the release tag, or "dev"
    Commit    string // full commit SHA
    BuildTime string // RFC 3339, UTC
)
```

The build target sets these with `-ldflags -X`. A binary built outside CI
reports `dev` and the working tree commit with a `-dirty` suffix when the tree
is not clean. The values feed the `/version` endpoint and the release evidence,
so a deployed build is traceable to one commit without guessing.

### Build target

`make build` produces `out/<service>`, statically linked, with symbol table and
DWARF stripped in release mode and kept in development mode. The output path is
fixed because the container image build copies from it.

## Acceptance criteria

1. A scaffolded module builds with `make build` on a clean clone with no network
   access beyond the module cache.
2. `out/<service> -version` prints version, commit, and build time.
3. A binary built from a dirty tree reports the `-dirty` suffix.
4. CI derives the Go version from `go.mod`; a grep test fails if a workflow
   hardcodes a Go version string.
5. `go vet ./...` is clean on the scaffold.

## Out of scope

Configuration loading, the server lifecycle, and the container image.
