import { describe, expect, it } from "vitest";

import { render } from "../src/entry-server";
import { find, publicRoutes, type Route } from "../src/lib/routes";
import { ROUTES } from "../src/routes/manifest";

const data = {
  locale: "en",
  origin: "https://example.com",
  siteName: "Service",
} as const;

describe("the render entry point", () => {
  it("returns the content a client that runs no JavaScript reads", () => {
    const route = find(ROUTES, "/docs")!;
    const result = render(route, data);
    expect(result.html).toContain("<h1>Documentation</h1>");
    expect(result.html).toContain("Every gate this repository runs is described here.");
    expect(result.head).toContain("<title>Documentation</title>");
    expect(result.head).toContain('name="description"');
    expect(result.path).toBe("/docs");
  });

  it("renders every public route", () => {
    for (const route of publicRoutes(ROUTES)) {
      const result = render(route, data);
      expect(result.html).toContain("<h1>");
      expect(result.head).toContain("<title>");
    }
  });

  it("follows the locale for the language and the direction", () => {
    const route = find(ROUTES, "/")!;
    expect(render(route, { ...data, locale: "ar" })).toMatchObject({
      lang: "ar",
      dir: "rtl",
    });
    expect(render(route, data).dir).toBe("ltr");
  });

  it("renders an application route with no head, because none is ever served", () => {
    const route = find(ROUTES, "/dashboard")!;
    const result = render(route, data);
    expect(result.html).toContain("<h1>Dashboard</h1>");
    expect(result.head).toBe("");
  });

  it("refuses a public route with no metadata and names it", () => {
    const route: Route = { path: "/pricing", surface: "public", component: () => null };
    expect(() => render(route, data)).toThrow(
      'route "/pricing" is public and declares no metadata',
    );
  });
});
