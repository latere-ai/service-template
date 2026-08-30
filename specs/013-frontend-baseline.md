---
title: "Frontend baseline: Bun, React, TypeScript, Vite, and Vitest"
status: drafted
depends_on:
  - specs/001-template-contract.md
affects: [skeleton/frontend/, skeleton/Makefile]
created: 2026-08-23
author: changkun
trigger: a consumer sets features.frontend in .template.yaml
---

# Frontend baseline

## Problem

Frontend setup is agreed on at the tool level and unspecified everywhere else.
Repositories share Bun, React, and Vite, and then differ on whether tests exist
at all, on the TypeScript strictness level, and on lint rules.

One failure mode deserves naming because it makes test results untrustworthy:
compiled `.js` files left beside their `.ts` sources. The module resolver
prefers the compiled file, the test runner therefore executes a stale build, and
the suite reports a pass for code that is not the code under review. The
symptom looks like a flaky test and the cause is invisible in the diff.

## Scope

Layer 2. The frontend project layout, TypeScript configuration, the test runner,
lint rules, and the build targets.

## Design

### Layout

```
frontend/
  src/
    routes/       route components, one file per route
    components/   shared presentation
    lib/          non-React logic, unit tested
    main.tsx      mount point
  tests/          integration and route tests
  public/         static assets copied verbatim
```

Logic lives in `lib/` and is testable without rendering. A component that holds
business logic cannot be tested cheaply, so the boundary is a structural rule.

### TypeScript

`strict` is on, together with `noUncheckedIndexedAccess`,
`exactOptionalPropertyTypes`, and `noImplicitOverride`. Build output never lands
next to sources: `noEmit` is set for type checking, and Vite owns the bundle.
`allowJs` is off, so a stray `.js` file in `src/` is a type error rather than a
silent shadow.

### The compiled-twin guard

Two defences, because the failure is quiet:

1. `.gitignore` excludes `src/**/*.js` and `src/**/*.js.map`.
2. A check target fails when any `.js` file exists beside a `.ts` or `.tsx` file
   in `src/`, and it runs in CI before the test job.

The guard runs before tests, so a contaminated tree fails with a clear message
instead of producing misleading results.

### Testing

Vitest with the DOM environment and the React testing library. Tests assert
behaviour through the accessible interface rather than through implementation
details, so a refactor that preserves behaviour does not rewrite the suite.
Coverage is measured and gated with the same threshold mechanism the Go side
uses.

### Lint and format

One shared ESLint configuration with the TypeScript, React hooks, and
accessibility rule sets, plus a formatter. The configuration is generated and
drift-checked like the Go lint configuration.

### Targets

| Target | Behaviour |
| --- | --- |
| `make frontend-install` | Installs from the lockfile, frozen |
| `make frontend-typecheck` | Type check with no emit |
| `make frontend-test` | Vitest with coverage gate |
| `make frontend-build` | Production bundle into `frontend/dist` |
| `make frontend-dev` | Development server with hot reload |

Install always uses the frozen lockfile, so CI cannot resolve a different
dependency tree than the developer did.

## Acceptance criteria

1. A scaffolded frontend installs, type checks, tests, and builds from a clean
   clone.
2. A `.js` file placed beside a `.ts` file in `src/` fails the guard target
   before tests run, with the path in the message.
3. Strict-mode violations fail the type check; a fixture proves each enabled
   flag.
4. The coverage gate fails below the threshold.
5. Install with a modified lockfile fails rather than resolving.

## Out of scope

Prerendering, the serving path, and internationalization.
