// The two documents a crawler fetches by a fixed name. Both are derived from
// the route manifest, so a route that is not declared public appears in
// neither.

import { publicRoutes, type Route } from "./routes";
import { absolute, escapeText } from "./metadata";

export interface SitemapOptions {
  readonly origin: string;
  /** lastModified is the build date. A document with no other change history
   * is still comparable between builds. */
  readonly lastModified?: Date;
}

/** buildSitemap returns the sitemap document for a manifest. It lists exactly
 * the public routes. */
export function buildSitemap(
  routes: readonly Route[],
  options: SitemapOptions,
): string {
  const stamp = (options.lastModified ?? new Date()).toISOString().slice(0, 10);
  const entries = publicRoutes(routes).map((route) => {
    const metadata = route.metadata;
    const parts = [
      `    <loc>${escapeText(absolute(options.origin, route.path))}</loc>`,
      `    <lastmod>${stamp}</lastmod>`,
    ];
    if (metadata?.changeFrequency !== undefined) {
      parts.push(`    <changefreq>${metadata.changeFrequency}</changefreq>`);
    }
    if (metadata?.priority !== undefined) {
      parts.push(`    <priority>${metadata.priority.toFixed(1)}</priority>`);
    }
    return `  <url>\n${parts.join("\n")}\n  </url>`;
  });
  return [
    `<?xml version="1.0" encoding="UTF-8"?>`,
    `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
    ...entries,
    `</urlset>`,
    ``,
  ].join("\n");
}

/** SITEMAP_NAME and ROBOTS_NAME are the fixed names crawlers request. The
 * serving layer gives both a short freshness lifetime, because they are small
 * and their staleness is visible to the client that reads them. */
export const SITEMAP_NAME = "sitemap.xml";
export const ROBOTS_NAME = "robots.txt";

/** buildRobots returns a robots document that points at the sitemap. A
 * sitemap no robots document references is a sitemap most crawlers never
 * fetch. */
export function buildRobots(options: SitemapOptions): string {
  return [
    `User-agent: *`,
    `Allow: /`,
    ``,
    `Sitemap: ${absolute(options.origin, `/${SITEMAP_NAME}`)}`,
    ``,
  ].join("\n");
}
