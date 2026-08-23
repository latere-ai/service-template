---
title: Internationalization: message catalogs, completeness, and formatting
status: drafted
depends_on:
  - specs/013-frontend-baseline.md
affects: [skeleton/frontend/src/i18n/, skeleton/internal/handler/]
created: 2026-08-23
author: changkun
trigger: a consumer sets features.i18n in .template.yaml
---

# Internationalization

## Problem

Internationalization added after the fact means auditing every string in the
codebase. Added at scaffold time it costs almost nothing. The recurring defect
is not the mechanism but the completeness: a developer adds a key to the base
locale, ships, and the other locales fall back silently. The user sees a mix of
languages, and no build step objected.

A second gap is the server side. Validation errors and notification text are
produced in Go, and they stay in one language even when the interface is
translated.

## Scope

Layer 2 and 3. Catalog format, the completeness gate, plural and formatting
rules, and the language negotiation shared by both sides.

## Design

### One catalog format, two consumers

Messages live in per-locale files in one directory, keyed by a dotted
identifier. The Go side and the frontend read the same files, so a string is
translated once. Message syntax supports named placeholders, plural selection,
and gender-neutral selection where a language needs it.

### Completeness gate

A check target compares every locale against the base locale and fails on a
missing key, an extra key, or a placeholder set that differs from the base. It
runs in CI. A locale cannot fall behind silently, which is the whole point.

Untranslated is different from missing. A key that is deliberately identical
across locales is marked, so the gate distinguishes "not yet translated" from
"translated to the same string".

### Language negotiation

One resolver, used by both sides: an explicit user preference wins, then a
cookie, then the `Accept-Language` header, then the default locale. Both sides
resolve identically, so a server-rendered error and the surrounding interface
never disagree.

### Formatting

Dates, numbers, and currency use the platform internationalization interfaces
with the resolved locale, never manual formatting. Time is stored and
transmitted in UTC and rendered in the viewer's zone, so a value does not shift
meaning between storage and display.

### Right-to-left and layout

The document direction follows the locale, and layout uses logical properties
rather than physical ones, so adding a right-to-left locale does not require a
second stylesheet.

## Acceptance criteria

1. A key added to the base locale and missing elsewhere fails the completeness
   gate, naming the locale and the key.
2. A placeholder mismatch between locales fails the gate.
3. A deliberately identical translation is marked and does not fail the gate.
4. Language negotiation produces the same locale on both sides for the same
   request; a test asserts it across all four precedence levels.
5. Plural selection is correct for a locale with more than two plural forms.
6. Switching to a right-to-left locale sets the document direction and requires
   no additional stylesheet.
