import { describe, expect, it } from "vitest";

import {
  buildRobots,
  buildSitemap,
  ROBOTS_NAME,
  SITEMAP_NAME,
} from "../src/lib/sitemap";
import { appRoutes, publicRoutes } from "../src/lib/routes";
import { ROUTES } from "../src/routes/manifest";

const options = {
  origin: "https://example.com",
  lastModified: new Date("2026-03-01T00:00:00Z"),
};

describe("the sitemap", () => {
  const sitemap = buildSitemap(ROUTES, options);

  it("lists exactly the public routes", () => {
    const listed = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]);
    expect(listed).toEqual(
      publicRoutes(ROUTES).map((r) => `https://example.com${r.path}`),
    );
  });

  it("lists no application route", () => {
    for (const route of appRoutes(ROUTES)) {
      expect(sitemap).not.toContain(route.path);
    }
  });

  it("carries the build date and the declared hints", () => {
    expect(sitemap).toContain("<lastmod>2026-03-01</lastmod>");
    expect(sitemap).toContain("<changefreq>weekly</changefreq>");
    expect(sitemap).toContain("<priority>1.0</priority>");
  });

  it("omits a hint a route does not declare", () => {
    const one = buildSitemap(
      [
        {
          path: "/plain",
          surface: "public",
          component: () => null,
          metadata: {
            title: "t",
            description: "d",
            canonical: "/plain",
            image: "/i.png",
            structuredData: {},
          },
        },
      ],
      options,
    );
    expect(one).not.toContain("<changefreq>");
    expect(one).not.toContain("<priority>");
  });

  it("stamps today when no build date is given", () => {
    const today = new Date().toISOString().slice(0, 10);
    expect(buildSitemap(ROUTES, { origin: "https://example.com" })).toContain(
      `<lastmod>${today}</lastmod>`,
    );
  });
});

describe("robots", () => {
  it("references the sitemap, which is what makes a crawler fetch it", () => {
    expect(buildRobots(options)).toContain(`https://example.com/${SITEMAP_NAME}`);
    expect(ROBOTS_NAME).toBe("robots.txt");
  });
});
