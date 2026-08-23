# Specs

One file per aspect of the design, named NNN-name.md. The number is a stable
identifier: it is never reused and never reassigned, so a reference to a spec
keeps resolving after the file is archived.

The lifecycle runs drafted, validated, dispatched, in-progress, testing,
complete. A spec reaches complete only with an Outcome section that records
what shipped and where it diverged from the design. A spec that is abandoned
or replaced becomes superseded.

Open specs are the work queue. Terminal specs move into `.archive/`, keeping
their number.

This file is generated from the frontmatter of the files below. Run
"make spec-index" after adding or changing a spec; "make spec-check" proves the
committed copy is current.

## Open

| Number | Spec | Status | Depends on |
| --- | --- | --- | --- |
| 001 | [Service baseline: the runtime, the surface, and the gates the service starts with](001-service-baseline.md) | complete | none |

## Archived

None.
