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

Add a row here when you add a spec. `make spec-check` proves every row agrees
with the spec it links to, that each status comes from the vocabulary above,
that no number is reused, and that nothing was built ahead of what it depends
on. A row that disagrees with its file fails the build, because a status
column a reader trusts and the code contradicts is worse than no column.

## Open

| Number | Spec | Status | Depends on |
| --- | --- | --- | --- |
| 001 | [Service baseline: the runtime, the surface, and the gates the service starts with](001-service-baseline.md) | complete | none |

## Archived

None.
