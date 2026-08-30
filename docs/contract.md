# The template contract

This document is normative. It states what the template owns, what a consumer
owns, how a consumer declares which template version it follows, and what
happens when the two disagree.

## Three layers

| Layer | Owner | Consumer holds | Update path |
| --- | --- | --- | --- |
| Workflows | Template | A caller file with inputs | Re-pin, or ride the moving major tag |
| Materialized files | Template | The generated file, committed | `template sync`, verified by `template check` |
| Libraries | Template | An import and a version | Dependency update |

Layer 2 files are committed in the consumer repository on purpose. A developer
must be able to read the lint rules and the hooks without network access, and
editors and `golangci-lint` expect them on disk.

## The consumer declaration

Every consumer repository holds `.template.yaml` at its root.

```yaml
template: github.com/latere-ai/service-template
version: v1.4.0          # exact template release the generated files came from
profile: service         # service | library | frontend-only, fixed at scaffold
features:
  frontend: true
  seo: true
  i18n: false
  database: true
  background: false
waivers: []              # generated files this repo deliberately diverges on
```

The quality gates are not declared here. Their thresholds, exemptions and
conventions live in `.lateregate.yaml`, which every generated repository
carries and which `latere.ai/x/ci-gate` reads. A repository has one quality
bar, and splitting it across two files makes the bar hard to read. An
exemption there also carries the reason it exists, which this declaration had
no place for.

A waiver entry is `{path, reason, expires}`. `template check` reports waived
files and fails on an expired waiver.

`template.lock` sits beside it and records a content digest per generated file.
It is written by the generator and never edited by hand.

## File modes

| Mode | Behaviour | Examples |
| --- | --- | --- |
| Generated | Rewritten by `sync`; drift is an error | `.lateregate.yaml`, git hooks, workflow callers |
| Seed | Written once at `init`; never rewritten, never checked | `main.go`, example handlers, `README.md` |
| Merged | Rewritten between markers; the consumer owns the rest | `Makefile`, `.gitignore` |

A merged file carries the owned region between these exact lines:

```
# >>> template: managed region, do not edit <<<
...
# >>> template: end managed region <<<
```

The generator rewrites what is between them and preserves everything outside.
A file whose markers are missing or unbalanced fails `check` with that reason,
because a silently unmanaged region is how a fix stops propagating.

## Drift outcomes

`template check` distinguishes three states, because the remedies differ:

| On disk | Lock | Template | Verdict | Exit |
| --- | --- | --- | --- | --- |
| matches lock | matches template | | clean | 0 |
| differs from lock | matches template | | **edited** locally | 3 |
| matches lock | differs from template | | **behind** the template | 4 |
| differs from lock | differs from template | | edited and behind | 3 |

Distinct exit codes let CI report the right instruction: revert or upstream the
edit, against run `template upgrade`.

## Determinism

Generation is a pure function of (template version, profile, features, and the
declared substitution values). The same inputs produce byte-identical output on
any machine and in any order. The drift check is meaningless otherwise, so the
generator is tested by generating twice and comparing.

## Version skew between layers

Workflows ride a moving major tag while materialized files are pinned exactly,
so a consumer can run a newer workflow against older generated files. Two rules
close the gap:

1. A reusable workflow declares the minimum `.template.yaml` version it needs.
   Its first job reads the consumer's version and fails with an upgrade
   instruction when the consumer is below it.
2. A workflow change that depends on a new or changed generated file raises that
   minimum. Raising the minimum is a minor release, and the release notes name
   the generated file.

A workflow may never read a generated file it did not declare a minimum for.

## Compatibility

The template publishes semantic version tags and a moving major tag. Inside a
major line:

- A new generated file, a new optional workflow input, or a new library function
  is a **minor** release.
- A change to a generated file that a consumer absorbs with `template sync` is a
  **minor** release.
- Removing a workflow input, renaming a required directory, or changing a gate
  so a previously passing repository fails is a **major** release.

Deprecations warn for one minor release before they break.

## Ownership boundary

The template owns the pipeline order, the quality gates, the runtime lifecycle
shape, the release evidence format, and the contents of generated files.

The consumer owns business logic, its deployment manifests, its live smoke
script, its feature selection, and every seed file.
