---
title: SEO and static distribution: build-time prerender, metadata, and cache policy
status: drafted
depends_on:
  - specs/013-frontend-baseline.md
affects: [skeleton/frontend/prerender/, skeleton/frontend/vite.config.ts]
created: 2026-08-23
author: changkun
trigger: a consumer sets features.seo in .template.yaml
---

# SEO and static distribution

## Problem

A single-page application returns one nearly empty HTML document for every
route. Search crawlers that execute JavaScript can still index it, but the
clients that increasingly matter do not execute JavaScript: answer engines,
social preview fetchers, and archive crawlers read the bytes in the first
response. For those clients an application-shell response contains no title, no
description, and no content.

Server rendering solves this and introduces a compute origin on the request
path, which removes the ability to serve the public surface from cache alone.

## Scope

Layer 2. The public and application surface split, the prerender step, metadata
generation, and cache policy.

## Design

### Two surfaces, one build

| Surface | Routes | Rendering | Reason |
| --- | --- | --- | --- |
| Public | Landing, documentation, pricing, articles | Prerendered to static HTML per route | Crawlers read the first response |
| Application | Everything behind authentication | Application shell only | No crawler ever sees it, and prerendering leaks nothing useful |

The split is declared in a route manifest. A route is prerendered only when the
manifest marks it public, so the application surface never enters the static
output by accident.

### Prerender step

After the client bundle is built, the build runs each public route through the
same React tree in a DOM-free renderer and writes a complete HTML document per
route. The rendered markup is the same code that hydrates in the browser, so the
static output cannot describe a different page from the interactive one.

The render entry point is a single module with a narrow signature: a route plus
its data returns HTML and head elements. Keeping it isolated means a later move
to per-request rendering changes where the function is called and not what it
does.

Prerendering is bounded by route count. It suits a surface with tens or hundreds
of routes. A surface with unbounded, content-generated routes needs on-demand
rendering with a cache, which is a separate decision this template does not
prejudge.

### Metadata

Each public route declares title, description, canonical URL, social preview
image, and structured data. The build emits them into the document head and
fails when a public route declares none, because a missing description is a
silent quality loss that no test would otherwise catch.

The build also emits `sitemap.xml` from the manifest and a `robots.txt` that
points at it.

### Cache policy

| Asset class | Policy | Reason |
| --- | --- | --- |
| Hashed bundles and images | Immutable, one year | The name changes when the content changes |
| Prerendered HTML | Short freshness with revalidation, entity tag | Content changes without a name change |
| `sitemap.xml`, `robots.txt` | Short freshness | Small, and staleness is visible to crawlers |

The output is a plain directory. It can be served from object storage behind a
content delivery network, or embedded into the service binary, and the cache
headers are the same either way, so the distribution choice is deployment
configuration rather than a code change.

## Acceptance criteria

1. Requesting a public route with JavaScript disabled returns the route content,
   its title, and its description in the response body.
2. An application route returns the shell only and appears in no static output.
3. A public route with no metadata declaration fails the build with the route
   name.
4. `sitemap.xml` lists exactly the public routes, and `robots.txt` references it.
5. Structured data on each public route validates against its schema.
6. Cache headers match the table for each asset class in a served-output test.
7. The prerendered markup hydrates with no mismatch warning.

## Out of scope

Per-request server rendering, and content authoring.
