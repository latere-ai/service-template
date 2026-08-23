import { describe, expect, it } from "vitest";

import type { Catalog } from "../src/i18n/catalog";
import { catalogs } from "../src/i18n/catalog";
import { check, checkAll, format } from "../src/i18n/completeness";

const base: Catalog = {
  locale: "en",
  identical: [],
  messages: {
    "app.name": "Nova",
    "greeting.hello": "Hello {name}",
    "cart.items": { one: "{count} item", other: "{count} items" },
  },
};

function other(overrides: Partial<Catalog>): Catalog {
  return {
    locale: "de",
    identical: [],
    messages: {
      "app.name": "Nova",
      "greeting.hello": "Hallo {name}",
      "cart.items": { one: "{count} Artikel", other: "{count} Artikel" },
    },
    ...overrides,
  };
}

// The base catalog above declares app.name identically in both locales, so a
// well-formed comparison locale marks it.
const complete = other({ identical: ["app.name"] });

describe("the completeness gate", () => {
  it("passes a complete locale", () => {
    expect(check(base, complete)).toEqual([]);
  });

  it("fails a key the base locale declares and this one does not", () => {
    const messages = { ...complete.messages };
    delete (messages as Record<string, unknown>)["greeting.hello"];
    const problems = check(base, other({ identical: ["app.name"], messages }));
    expect(problems).toHaveLength(1);
    expect(problems[0]?.locale).toBe("de");
    expect(problems[0]?.key).toBe("greeting.hello");
    expect(format(problems[0]!)).toContain("missing");
  });

  it("fails a placeholder set that differs from the base", () => {
    const problems = check(
      base,
      other({
        identical: ["app.name"],
        messages: { ...complete.messages, "greeting.hello": "Hallo {vorname}" },
      }),
    );
    expect(problems.map((p) => p.key)).toContain("greeting.hello");
    expect(format(problems[0]!)).toContain("placeholders are vorname");
  });

  it("passes a translation that is deliberately identical and marked", () => {
    expect(check(base, complete).map((p) => p.key)).not.toContain("app.name");
  });

  it("fails an identical translation nobody marked, because it reads as untranslated", () => {
    const problems = check(base, other({ identical: [] }));
    expect(problems).toHaveLength(1);
    expect(problems[0]?.key).toBe("app.name");
    expect(format(problems[0]!)).toContain("identical");
  });

  it("fails a mark that no longer matches the base locale", () => {
    const problems = check(
      base,
      other({
        identical: ["app.name", "greeting.hello"],
        messages: { ...complete.messages },
      }),
    );
    expect(problems.map((p) => p.key)).toEqual(["greeting.hello"]);
    expect(format(problems[0]!)).toContain("differs from the base locale");
  });

  it("fails a mark for a key the catalog does not declare", () => {
    const messages = { ...complete.messages };
    delete (messages as Record<string, unknown>)["app.name"];
    const problems = check(base, other({ identical: ["app.name"], messages }));
    expect(problems.map((p) => p.key)).toEqual(["app.name", "app.name"]);
  });

  it("fails a key the base locale does not declare", () => {
    const problems = check(
      base,
      other({
        identical: ["app.name"],
        messages: { ...complete.messages, extra: "x" },
      }),
    );
    expect(problems.map((p) => p.key)).toEqual(["extra"]);
    expect(format(problems[0]!)).toContain("not in the base locale en");
  });

  it("fails a plural form the language uses and the catalog omits", () => {
    const arabic: Catalog = {
      locale: "ar",
      identical: ["app.name"],
      messages: {
        "app.name": "Nova",
        "greeting.hello": "مرحبا {name}",
        "cart.items": { one: "{count}", other: "{count}" },
      },
    };
    const problems = check(base, arabic);
    expect(problems.map((p) => p.reason)).toEqual([
      'plural form "zero" is missing, ar uses it',
      'plural form "two" is missing, ar uses it',
      'plural form "few" is missing, ar uses it',
      'plural form "many" is missing, ar uses it',
    ]);
  });

  it("fails a message that is plural on one side and a single string on the other", () => {
    const flat = check(
      base,
      other({
        identical: ["app.name"],
        messages: { ...complete.messages, "cart.items": "{count} Artikel" },
      }),
    );
    expect(flat.map((p) => p.reason)).toContain(
      "is a single string, the base locale declares plural forms",
    );

    const plural = check(
      { ...base, messages: { ...base.messages, "greeting.hello": "Hello {name}" } },
      other({
        identical: ["app.name"],
        messages: {
          ...complete.messages,
          "greeting.hello": { one: "Hallo {name}", other: "Hallo {name}" },
        },
      }),
    );
    expect(plural.map((p) => p.reason)).toContain(
      "declares plural forms, the base locale is a single string",
    );
  });

  it("reports nothing when there is no catalog at all", () => {
    expect(checkAll([])).toEqual([]);
  });

  it("passes the catalogs this repository ships", () => {
    expect(checkAll(catalogs()).map(format)).toEqual([]);
  });
});
