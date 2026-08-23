// The route manifest type and the rules the build enforces on it.
//
// The manifest is the single place that says which routes a crawler may see.
// A route is prerendered because it is marked public here and for no other
// reason, so the application surface cannot reach the static output by
// accident.

import type { ComponentType } from "react";

import { validate as validateStructuredData, type JsonLd } from "./structured-data";

/** Surface splits the two rendering treatments. A public route is rendered to
 * a complete document at build time; an application route ships the shell and
 * renders after authentication in the browser. */
export type Surface = "public" | "app";

/** ChangeFrequency is the sitemap hint for how often a document changes. */
export type ChangeFrequency =
  "always" | "hourly" | "daily" | "weekly" | "monthly" | "yearly" | "never";

/** Metadata is what a client that runs no JavaScript reads. Every field is
 * required on a public route, because each one is read by a different class of
 * client and a missing one is a silent quality loss. */
export interface Metadata {
  readonly title: string;
  readonly description: string;
  /** canonical is the absolute path of the document, origin excluded. */
  readonly canonical: string;
  /** image is the social preview image, as an absolute path. */
  readonly image: string;
  readonly structuredData: JsonLd;
  readonly changeFrequency?: ChangeFrequency;
  /** priority is the sitemap priority, between 0 and 1. */
  readonly priority?: number;
}

/** Route is one entry of the manifest. */
export interface Route {
  readonly path: string;
  readonly surface: Surface;
  readonly component: ComponentType;
  /** metadata is required on a public route. The type keeps an author from
   * omitting it, and `validate` keeps a manifest built at run time from
   * omitting it too. */
  readonly metadata?: Metadata;
}

/** publicRoutes returns the routes the build renders to static documents. */
export function publicRoutes(routes: readonly Route[]): readonly Route[] {
  return routes.filter((r) => r.surface === "public");
}

/** appRoutes returns the routes that ship the shell only. */
export function appRoutes(routes: readonly Route[]): readonly Route[] {
  return routes.filter((r) => r.surface === "app");
}

/** find returns the route for a path. The match is exact; a path the manifest
 * does not hold is a client-side 404. */
export function find(routes: readonly Route[], path: string): Route | undefined {
  const normalized = path === "" ? "/" : path.replace(/\/+$/, "") || "/";
  return routes.find((r) => r.path === normalized);
}

/** validate returns the problems in a manifest. Each problem names the route,
 * because the fix is in that route's declaration.
 *
 * A public route without metadata is the failure this exists for: it builds,
 * it serves, and it is invisible to every client that reads the first response
 * and nothing else. */
export function validate(routes: readonly Route[]): string[] {
  const problems: string[] = [];
  const seen = new Set<string>();
  for (const route of routes) {
    if (!route.path.startsWith("/")) {
      problems.push(`route "${route.path}" is not an absolute path`);
    }
    if (seen.has(route.path)) {
      problems.push(`route "${route.path}" is declared more than once`);
    }
    seen.add(route.path);

    if (route.surface !== "public") {
      if (route.metadata !== undefined) {
        problems.push(
          `route "${route.path}" is an application route and declares metadata, which would never be served`,
        );
      }
      continue;
    }
    const metadata = route.metadata;
    if (metadata === undefined) {
      problems.push(`route "${route.path}" is public and declares no metadata`);
      continue;
    }
    for (const field of ["title", "description", "canonical", "image"] as const) {
      if (metadata[field].trim() === "") {
        problems.push(`route "${route.path}" declares an empty ${field}`);
      }
    }
    if (metadata.canonical !== route.path) {
      problems.push(
        `route "${route.path}" declares canonical "${metadata.canonical}", which is a different document`,
      );
    }
    if (
      metadata.priority !== undefined &&
      (metadata.priority < 0 || metadata.priority > 1)
    ) {
      problems.push(
        `route "${route.path}" declares priority ${metadata.priority}, which is outside 0 to 1`,
      );
    }
    problems.push(
      ...validateStructuredData(metadata.structuredData).map(
        (p) => `route "${route.path}": ${p}`,
      ),
    );
  }
  return problems;
}
