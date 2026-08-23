import { describe, expect, it } from "vitest";

import {
  catalog,
  catalogs,
  interpolate,
  placeholders,
  pluralCategories,
  resolveMessage,
  select,
  translate,
} from "../src/i18n/catalog";
import { BASE_LOCALE, SUPPORTED_LOCALES } from "../src/i18n/locales";

describe("catalogs", () => {
  it("ships one catalog per supported locale, base locale first", () => {
    const all = catalogs();
    expect(all).toHaveLength(SUPPORTED_LOCALES.length);
    expect(all[0]?.locale).toBe(BASE_LOCALE);
  });

  it("reads a locale by name", () => {
    expect(catalog("de").messages["nav.docs"]).toBe("Dokumentation");
  });
});

describe("placeholders", () => {
  it("reads the names a plain message uses", () => {
    expect([...placeholders("{year} {name}")].sort()).toEqual(["name", "year"]);
  });

  it("unions the names across the plural forms", () => {
    expect(
      [...placeholders({ one: "{count} of {total}", other: "{count}" })].sort(),
    ).toEqual(["count", "total"]);
  });
});

describe("plural selection", () => {
  it("uses every form a locale with more than two of them has", () => {
    const categories = pluralCategories("ar");
    expect(categories).toEqual(["zero", "one", "two", "few", "many", "other"]);
  });

  // Arabic selects six forms, which is the case a two-form assumption breaks
  // on. The counts below are one per form.
  it.each([
    [0, "zero"],
    [1, "one"],
    [2, "two"],
    [3, "few"],
    [11, "many"],
    [100, "other"],
  ])("selects the form for %i", (count, category) => {
    const message = {
      zero: "zero",
      one: "one",
      two: "two",
      few: "few",
      many: "many",
      other: "other",
    };
    expect(select("ar", message, count)).toBe(category);
  });

  it("falls back to the other form when a locale omits one", () => {
    expect(select("ar", { other: "other" }, 2)).toBe("other");
  });

  it("returns an empty string when a message holds no form at all", () => {
    expect(select("en", {}, 1)).toBe("");
  });
});

describe("interpolate", () => {
  it("formats a number with the locale's own digits", () => {
    expect(interpolate("en", "{count} items", { count: 1234 })).toBe("1,234 items");
    expect(interpolate("de", "{count} Stück", { count: 1234 })).toBe("1.234 Stück");
  });

  it("leaves an unknown placeholder in place so the gap is visible", () => {
    expect(interpolate("en", "{missing} here", {})).toBe("{missing} here");
  });
});

describe("translate", () => {
  it("resolves a plain message in each locale", () => {
    expect(translate("en", "nav.docs")).toBe("Documentation");
    expect(translate("de", "nav.docs")).toBe("Dokumentation");
  });

  it("resolves a plural message with the locale's rules", () => {
    expect(translate("en", "notifications.count", { count: 1 })).toBe("1 notification");
    expect(translate("en", "notifications.count", { count: 5 })).toBe(
      "5 notifications",
    );
    expect(translate("de", "notifications.count", { count: 1 })).toBe(
      "1 Benachrichtigung",
    );
  });

  it("selects the Arabic dual form, which a two-form catalog cannot express", () => {
    expect(translate("ar", "notifications.count", { count: 2 })).toBe("إشعاران");
  });

  it("returns the key when no locale declares it", () => {
    expect(translate("en", "nothing.here")).toBe("nothing.here");
  });

  it("falls back to the base locale for a key one locale omits", () => {
    const base = { locale: "en", identical: [], messages: { "a.b": "Hello" } };
    const partial = { locale: "de", identical: [], messages: {} };
    expect(resolveMessage(partial, base, "a.b")).toEqual({
      message: "Hello",
      source: "en",
    });
    expect(resolveMessage(base, base, "a.b")?.source).toBe("en");
    expect(resolveMessage(partial, base, "missing")).toBeUndefined();
  });
});
