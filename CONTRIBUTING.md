# Contributing

## Workflow

Work in this repository is spec-driven. Before you change behaviour, there is a
spec in [`specs/`](specs/README.md) that describes the problem, the design, and
the acceptance criteria. Read it, then implement it.

1. Pick a spec whose `status` is `drafted` and whose `depends_on` entries are
   complete.
2. Implement the acceptance criteria. Add tests in the same change.
3. Update the spec with an Outcome section and set `status: complete`.

For a change with no spec, open an issue first and say which aspect of the
template it touches. A change that adds a new aspect needs a new spec.

## Standards

The template holds itself to the standards it ships:

- `make fmt-check lint test` passes before you push.
- Every bug fix carries a test that fails without the fix.
- Coverage stays above the threshold the coverage spec sets.
- Documentation changes reach every locale in the same change.

## Commit messages

`scope: lowercase description`. One logical change per commit.

## Reporting problems

Use GitHub issues for defects and proposals. Use the process in
[SECURITY.md](SECURITY.md) for anything with a security impact.
