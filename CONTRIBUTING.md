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
template it touches. A change that adds a new aspect needs a new spec, and a
spec is a reasonable first contribution on its own.

## Standards

The template holds itself to the standards it ships, so CI checks the same
things a consumer's CI would:

- `make fmt-check lint test` is what CI runs. Running it first saves you a
  round trip.
- A bug fix wants a test that fails without the fix. That test is how the fix
  stays fixed.
- Coverage has a threshold, and the build reports where you landed.
- A change to translated text needs every locale, because the completeness gate
  fails on a locale left behind.

If you hit a genuine exception to any of these, say so in the pull request
rather than working around the check.

## Commit messages

`scope: lowercase description`. One logical change per commit.

## Reporting problems

Use GitHub issues for defects and proposals. Use the process in
[SECURITY.md](SECURITY.md) for anything with a security impact.
