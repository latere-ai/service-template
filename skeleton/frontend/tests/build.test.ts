// @vitest-environment node
//
// The built output, read the way a client that runs no JavaScript reads it.
// Everything here is asserted against files the production build produced,
// because the point of the prerender is what is in the first response.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeAll, describe, expect, it } from "vitest";

import { publicRoutes } from "../src/lib/routes";
import { ROUTES } from "../src/routes/manifest";
import { run } from "./run";

const DIST = "dist";

function document(name: string): string {
  return readFileSync(join(DIST, name), "utf8");
}

beforeAll(() => {
  const result = run("bun", ["run", "build"]);
  expect(result.output).not.toContain("error");
  expect(result.status).toBe(0);
}, 5 * 60_000);

describe("a public route", () => {
  it.each([
    ["index.html", "A service that starts complete"],
    ["docs/index.html", "Every gate this repository runs is described here."],
    ["pricing/index.html", "One plan, billed per month."],
  ])("%s carries its content in the response body", (file, content) => {
    expect(document(file)).toContain(content);
  });

  it.each(publicRoutes(ROUTES))("$path carries its title and description", (route) => {
    const file =
      route.path === "/" ? "index.html" : `${route.path.slice(1)}/index.html`;
    const html = document(file);
    expect(html).toContain(`<title>${route.metadata?.title}</title>`);
    expect(html).toContain(route.metadata?.description ?? "");
    expect(html).toContain('<script type="application/ld+json">');
  });

  it("carries the hashed bundle the build produced", () => {
    const html = document("index.html");
    const script = /<script type="module"[^>]*src="([^"]+)"/.exec(html);
    expect(script?.[1]).toMatch(/^\/assets\/.+-[A-Za-z0-9_-]{8,}\.js$/);
    expect(existsSync(join(DIST, script![1]!.slice(1)))).toBe(true);
  });

  it("records the route its markup was rendered for", () => {
    expect(document("docs/index.html")).toContain('data-prerender-path="/docs"');
  });
});

describe("an application route", () => {
  it("has no document of its own", () => {
    expect(existsSync(join(DIST, "dashboard"))).toBe(false);
  });

  it("appears in no static output", () => {
    for (const file of ["index.html", "docs/index.html", "pricing/index.html"]) {
      expect(document(file)).not.toContain("Signed-in work happens here.");
    }
  });

  it("is served the shell, which the build leaves at the document root", () => {
    expect(existsSync(join(DIST, "index.html"))).toBe(true);
  });
});

describe("the crawler documents", () => {
  it("list exactly the public routes", () => {
    const sitemap = document("sitemap.xml");
    const listed = [...sitemap.matchAll(/<loc>https?:\/\/[^/<]+([^<]*)<\/loc>/g)].map(
      (m) => m[1],
    );
    expect(listed).toEqual(publicRoutes(ROUTES).map((r) => r.path));
    expect(sitemap).not.toContain("/dashboard");
  });

  it("point a crawler at the sitemap", () => {
    expect(document("robots.txt")).toContain("Sitemap: ");
    expect(document("robots.txt")).toContain("sitemap.xml");
  });
});

describe("the prerender step", () => {
  // The landing route is written over the document the bundler produced, so a
  // second run has no template. It says so instead of writing a document with
  // no content in it.
  it("refuses to run twice without the bundler between the runs", () => {
    const result = run("bun", ["run", "prerender/prerender.ts"]);
    expect(result.status).not.toBe(0);
    expect(result.output).toContain("run the bundler first");
  });
});
