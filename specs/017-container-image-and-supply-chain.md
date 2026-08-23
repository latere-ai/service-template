---
title: Container image and supply chain: reproducible build, SBOM, provenance, signature
status: drafted
depends_on:
  - specs/002-go-module-baseline.md
  - specs/015-spa-serving-and-asset-pinning.md
affects: [skeleton/Dockerfile, skeleton/Dockerfile.ci, .github/workflows/release.yml]
created: 2026-08-23
author: changkun
trigger: foundation spec for any deployed service
---

# Container image and supply chain

## Problem

An image built by a developer and an image built by CI are usually different
artifacts produced by different steps, so what runs in production is not what
was tested. Images ship unsigned, with no bill of materials and no link back to
the commit and workflow that produced them, so a consumer cannot verify origin
and an operator cannot answer which advisory affects which running image.

## Scope

Layer 1 and 2. The two build paths, the base image policy, and the three
attestations.

## Design

### Two Dockerfiles, one artifact

| File | Used by | Behaviour |
| --- | --- | --- |
| `Dockerfile` | Developers | Multi-stage, compiles inside the image, self-contained |
| `Dockerfile.ci` | Release pipeline | Copies the prebuilt binary from `out/` into the runtime stage |

The pipeline compiles once in the verify job and packages that exact binary, so
the tested artifact and the shipped artifact are the same bytes. Both files
share the runtime stage through a common base definition, so the developer image
and the released image differ only in how the binary arrived.

### Runtime image

A minimal distribution with no shell and no package manager, running as a
non-root user with a read-only root filesystem and no capabilities. Only the
binary, the certificate bundle, and the time zone database are present. Removing
the shell removes the most common post-compromise foothold, and it costs nothing
because debugging happens through an ephemeral debug container.

### Reproducibility

The build sets a fixed build timestamp derived from the commit, strips paths
from the binary, and pins the base image by digest rather than by tag. A tag can
move; a digest cannot. Two builds of the same commit produce the same image
digest, which is what makes the provenance claim checkable rather than
decorative.

### Attestations

| Artifact | Content | Verified by |
| --- | --- | --- |
| Bill of materials | Every Go module and every frontend package with version and license | Advisory matching and license review |
| Provenance | Build workflow, commit, and inputs, in the standard attestation format | Consumers verifying the image came from this pipeline |
| Signature | Keyless signature over the image digest | Admission policy at deploy time |

All three are attached to the image in the registry, and the release pipeline
verifies them after push. An attestation that is produced and never verified
does not prove anything, so verification is part of the pipeline and not a
manual step.

### Image scanning

The pushed image is scanned for operating-system and language vulnerabilities.
A finding above the configured severity fails the release before deploy, with
the same suppression rules the static analysis spec defines.

### Multi-architecture

Images build for both common server architectures. Local development on either
architecture then uses the same image the servers run.

## Acceptance criteria

1. Two builds of one commit produce the same image digest.
2. The runtime image contains no shell and no package manager; a test asserts it.
3. The image runs as a non-root user with a read-only root filesystem.
4. The bill of materials lists both Go modules and frontend packages.
5. Signature and provenance verification runs in the pipeline and fails on a
   tampered digest; a test proves the failure path.
6. A high-severity image finding fails the release before the deploy step.
7. Both architectures are present in the published manifest list.
