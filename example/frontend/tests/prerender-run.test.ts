// @vitest-environment node
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { BODY_SLOT, HEAD_SLOT } from "../prerender/document";
import { prerenderAll } from "../prerender/run";
import { PRERENDER_ATTRIBUTE } from "../src/lib/mount";
import type { Route } from "../src/lib/routes";
import { SCHEMA_CONTEXT } from "../src/lib/structured-data";
import { ROUTES } from "../src/routes/manifest";

const template = `<!doctype html>
<html lang="en" dir="ltr">
  <head>${HEAD_SLOT}</head>
  <body><div id="root" ${PRERENDER_ATTRIBUTE}="">${BODY_SLOT}</div></body>
</html>
`;

function dist(): string {
  const directory = mkdtempSync(join(tmpdir(), "prerender-"));
  writeFileSync(join(directory, "index.html"), template);
  return directory;
}

const options = {
  origin: "https://example.com",
  siteName: "Service",
  locale: "en",
  lastModified: new Date("2026-03-01T00:00:00Z"),
} as const;

describe("prerenderAll", () => {
  it("writes one document per public route and the two crawler documents", async () => {
    const directory = dist();
    const written = await prerenderAll(ROUTES, { ...options, dist: directory });
    expect(written).toEqual([
      "index.html",
      "docs/index.html",
      "pricing/index.html",
      "sitemap.xml",
      "robots.txt",
    ]);
    expect(existsSync(join(directory, "dashboard"))).toBe(false);
    const docs = readFileSync(join(directory, "docs", "index.html"), "utf8");
    expect(docs).toContain("<title>Documentation</title>");
    expect(docs).toContain("<h1>Documentation</h1>");
  });

  it("renders every document in the locale it is given", async () => {
    const directory = dist();
    await prerenderAll(ROUTES, { ...options, dist: directory, locale: "ar" });
    const landing = readFileSync(join(directory, "index.html"), "utf8");
    expect(landing).toContain('<html lang="ar" dir="rtl">');
    expect(landing).toContain("خدمة تبدأ مكتملة");
  });

  // The failure this step exists to catch. A route that ships without
  // metadata builds, serves, and is invisible to every client that reads the
  // first response and nothing else.
  it("fails the build when a public route declares no metadata, and names it", async () => {
    const routes: Route[] = [
      ...ROUTES,
      { path: "/changelog", surface: "public", component: () => null },
    ];
    await expect(prerenderAll(routes, { ...options, dist: dist() })).rejects.toThrow(
      'route "/changelog" is public and declares no metadata',
    );
  });

  it("fails the build when a route declares structured data that does not validate", async () => {
    const routes: Route[] = [
      {
        path: "/changelog",
        surface: "public",
        component: () => null,
        metadata: {
          title: "Changelog",
          description: "What changed",
          canonical: "/changelog",
          image: "/social-card.png",
          structuredData: { "@context": SCHEMA_CONTEXT, "@type": "WebPage", name: "x" },
        },
      },
    ];
    await expect(prerenderAll(routes, { ...options, dist: dist() })).rejects.toThrow(
      'route "/changelog": structuredData of type WebPage is missing "description"',
    );
  });

  it("refuses to run twice without the bundler between the runs", async () => {
    const directory = dist();
    await prerenderAll(ROUTES, { ...options, dist: directory });
    await expect(prerenderAll(ROUTES, { ...options, dist: directory })).rejects.toThrow(
      "run the bundler first",
    );
  });

  it("stamps every document with a build date when none is given", async () => {
    const directory = dist();
    await prerenderAll(ROUTES, {
      dist: directory,
      origin: options.origin,
      siteName: options.siteName,
      locale: options.locale,
    });
    const today = new Date().toISOString().slice(0, 10);
    expect(readFileSync(join(directory, "sitemap.xml"), "utf8")).toContain(today);
  });
});
