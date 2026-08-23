import { describe, expect, it } from "vitest";

import { appRoutes, find, publicRoutes, validate, type Route } from "../src/lib/routes";
import { SCHEMA_CONTEXT } from "../src/lib/structured-data";
import { ROUTES } from "../src/routes/manifest";

function Stub() {
  return null;
}

function metadata(path: string) {
  return {
    title: "Title",
    description: "Description",
    canonical: path,
    image: "/social-card.png",
    structuredData: {
      "@context": SCHEMA_CONTEXT,
      "@type": "WebPage" as const,
      name: "Title",
      description: "Description",
    },
  };
}

describe("the shipped manifest", () => {
  it("is valid", () => {
    expect(validate(ROUTES)).toEqual([]);
  });

  it("splits the public surface from the application surface", () => {
    expect(publicRoutes(ROUTES).map((r) => r.path)).toEqual(["/", "/docs", "/pricing"]);
    expect(appRoutes(ROUTES).map((r) => r.path)).toEqual(["/dashboard"]);
  });

  it("declares no metadata on the application surface, which is never crawled", () => {
    for (const route of appRoutes(ROUTES)) {
      expect(route.metadata).toBeUndefined();
    }
  });
});

describe("find", () => {
  it("matches a path exactly and tolerates a trailing separator", () => {
    expect(find(ROUTES, "/docs")?.path).toBe("/docs");
    expect(find(ROUTES, "/docs/")?.path).toBe("/docs");
    expect(find(ROUTES, "/")?.path).toBe("/");
    expect(find(ROUTES, "")?.path).toBe("/");
  });

  it("returns nothing for a path the manifest does not hold", () => {
    expect(find(ROUTES, "/nowhere")).toBeUndefined();
  });
});

describe("manifest validation", () => {
  // This is the failure the gate exists for: the route builds, it serves, and
  // every client that reads only the first response sees nothing about it.
  it("fails a public route that declares no metadata, and names the route", () => {
    const routes: Route[] = [{ path: "/pricing", surface: "public", component: Stub }];
    expect(validate(routes)).toEqual([
      'route "/pricing" is public and declares no metadata',
    ]);
  });

  it("fails an application route that declares metadata that would never be served", () => {
    const routes: Route[] = [
      {
        path: "/dashboard",
        surface: "app",
        component: Stub,
        metadata: metadata("/dashboard"),
      },
    ];
    expect(validate(routes)[0]).toContain("would never be served");
  });

  it.each(["title", "description", "canonical", "image"] as const)(
    "fails an empty %s",
    (field) => {
      const routes: Route[] = [
        {
          path: "/docs",
          surface: "public",
          component: Stub,
          metadata: { ...metadata("/docs"), [field]: "  " },
        },
      ];
      expect(validate(routes).some((p) => p.includes(field))).toBe(true);
    },
  );

  it("fails a canonical that names a different document", () => {
    const routes: Route[] = [
      {
        path: "/docs",
        surface: "public",
        component: Stub,
        metadata: { ...metadata("/docs"), canonical: "/other" },
      },
    ];
    expect(validate(routes)[0]).toContain('canonical "/other"');
  });

  it("fails a relative path, a duplicate, and a priority outside its range", () => {
    const routes: Route[] = [
      { path: "docs", surface: "app", component: Stub },
      { path: "/docs", surface: "app", component: Stub },
      { path: "/docs", surface: "app", component: Stub },
      {
        path: "/pricing",
        surface: "public",
        component: Stub,
        metadata: { ...metadata("/pricing"), priority: 2 },
      },
    ];
    const problems = validate(routes);
    expect(problems).toContain('route "docs" is not an absolute path');
    expect(problems).toContain('route "/docs" is declared more than once');
    expect(problems.some((p) => p.includes("priority 2"))).toBe(true);
  });

  it("reports a structured data problem against the route it belongs to", () => {
    const routes: Route[] = [
      {
        path: "/docs",
        surface: "public",
        component: Stub,
        metadata: {
          ...metadata("/docs"),
          structuredData: {
            "@context": SCHEMA_CONTEXT,
            "@type": "WebPage",
            name: "Title",
          },
        },
      },
    ];
    expect(validate(routes)[0]).toBe(
      'route "/docs": structuredData of type WebPage is missing "description"',
    );
  });
});
