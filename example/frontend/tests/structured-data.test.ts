import { describe, expect, it } from "vitest";

import { SCHEMA_CONTEXT, supportedTypes, validate } from "../src/lib/structured-data";
import { publicRoutes } from "../src/lib/routes";
import { ROUTES } from "../src/routes/manifest";

describe("structured data validation", () => {
  it("accepts a complete node", () => {
    expect(
      validate({
        "@context": SCHEMA_CONTEXT,
        "@type": "WebSite",
        name: "Service",
        url: "/",
      }),
    ).toEqual([]);
  });

  it("names the property a node is missing", () => {
    const problems = validate({
      "@context": SCHEMA_CONTEXT,
      "@type": "WebSite",
      name: "Service",
    });
    expect(problems).toEqual(['structuredData of type WebSite is missing "url"']);
  });

  it("treats an empty value as missing, because a crawler reads nothing either way", () => {
    expect(
      validate({ "@context": SCHEMA_CONTEXT, "@type": "WebSite", name: "", url: "/" }),
    ).toEqual(['structuredData of type WebSite is missing "name"']);
  });

  it("rejects a node that is not an object", () => {
    expect(validate("nope")).toEqual(["structuredData is not a JSON-LD object"]);
    expect(validate(null)).toEqual(["structuredData is not a JSON-LD object"]);
  });

  it("rejects the wrong vocabulary and a missing type", () => {
    expect(
      validate({
        "@context": "https://example.org",
        "@type": "WebSite",
        name: "n",
        url: "/",
      })[0],
    ).toContain("@context");
    expect(validate({ "@context": SCHEMA_CONTEXT })).toContain(
      "structuredData declares no @type",
    );
  });

  it("refuses a type it has no rule for, rather than passing it silently", () => {
    const problems = validate({ "@context": SCHEMA_CONTEXT, "@type": "Sandwich" });
    expect(problems[0]).toContain("no rule for");
    expect(problems[0]).toContain(supportedTypes()[0]);
  });

  it("validates a nested node and names where it is", () => {
    expect(
      validate({
        "@context": SCHEMA_CONTEXT,
        "@type": "Product",
        name: "Service",
        offers: { "@type": "Offer", price: "49.00" },
      }),
    ).toEqual(['structuredData.offers of type Offer is missing "priceCurrency"']);
  });

  it("validates each element of a nested list", () => {
    expect(
      validate({
        "@context": SCHEMA_CONTEXT,
        "@type": "FAQPage",
        mainEntity: [
          { "@type": "Offer", price: "1", priceCurrency: "EUR" },
          { "@type": "Offer" },
        ],
      }),
    ).toEqual([
      'structuredData.mainEntity[1] of type Offer is missing "price"',
      'structuredData.mainEntity[1] of type Offer is missing "priceCurrency"',
    ]);
  });

  it("validates the structured data every shipped public route declares", () => {
    for (const route of publicRoutes(ROUTES)) {
      expect(validate(route.metadata?.structuredData)).toEqual([]);
    }
  });
});
