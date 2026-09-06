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

- `make all` is what CI runs, target for target: the pipeline probes this
  repository's Makefile and runs what it finds, so there is no second list of
  commands anywhere. Running it first saves you a round trip.
- A bug fix wants a test that fails without the fix. That test is how the fix
  stays fixed.
- Coverage has a threshold, and the build reports where you landed.
- A change to translated text needs every locale, because the completeness gate
  fails on a locale left behind.

If you hit a genuine exception to any of these, say so in the pull request
rather than working around the check.

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
  this file, package documentation, commit messages, source comments.
  Precise, in the project's own terms, with the reason a design is what it
  is.
- Developer, someone debugging a running system: logs, traces, startup
  failures. Exact and complete: object, operation, observed value, expected
  value, and the underlying error.

An error has one code, one fixed user sentence in `message`, and one
developer detail in a separate field shown only on request. The canonical
statement, worked examples, and the review checklist are in the registers
document in pkg:
https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md
The rule applies to new text and to reviews; existing text is fixed as it is
touched.

## Reporting problems

Use GitHub issues for defects and proposals. Use the process in
[SECURITY.md](SECURITY.md) for anything with a security impact.
