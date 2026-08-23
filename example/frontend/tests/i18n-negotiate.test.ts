import { describe, expect, it } from "vitest";

import cases from "../src/i18n/negotiation-cases.json";
import {
  fromBrowser,
  LOCALE_COOKIE,
  match,
  parseAcceptLanguage,
  readCookie,
  resolve,
  resolveLocale,
} from "../src/i18n/negotiate";

interface Vector {
  readonly name: string;
  readonly preference: string | null;
  readonly cookie: string | null;
  readonly acceptLanguage: string | null;
  readonly supported: string[];
  readonly defaultLocale: string;
  readonly expect: string;
}

// The vectors are the shared contract. The server side runs the same file, so
// a request cannot resolve to one locale in the interface and another in a
// server-produced message.
describe("the shared negotiation vectors", () => {
  const vectors = cases.cases as Vector[];

  it("covers every precedence level", () => {
    expect(vectors.length).toBeGreaterThanOrEqual(4);
  });

  it.each(vectors)("$name", (vector) => {
    expect(
      resolve(
        {
          preference: vector.preference,
          cookie: vector.cookie,
          acceptLanguage: vector.acceptLanguage,
        },
        { supported: vector.supported, defaultLocale: vector.defaultLocale },
      ),
    ).toBe(vector.expect);
  });
});

describe("parseAcceptLanguage", () => {
  it("orders by quality and keeps header order for a tie", () => {
    expect(parseAcceptLanguage("en;q=0.5,de,fr;q=0.9,es")).toEqual([
      "de",
      "es",
      "fr",
      "en",
    ]);
  });

  it("drops a refused language and an empty entry", () => {
    expect(parseAcceptLanguage("ar;q=0,,de")).toEqual(["de"]);
  });

  it("ignores a parameter that is not a quality", () => {
    expect(parseAcceptLanguage("de;charset=utf-8")).toEqual(["de"]);
  });

  it("drops an entry whose quality cannot be read as a number", () => {
    expect(parseAcceptLanguage("de;q=1.2.3,en")).toEqual(["en"]);
  });
});

describe("match", () => {
  it("prefers an exact match over a language match", () => {
    expect(match("de-AT", ["de", "de-AT"])).toBe("de-AT");
  });

  it("falls back to the first supported locale of the language", () => {
    expect(match("de-CH", ["en", "de", "de-AT"])).toBe("de");
  });

  it("matches nothing for a wildcard or an empty tag", () => {
    expect(match("*", ["en"])).toBeUndefined();
    expect(match("  ", ["en"])).toBeUndefined();
  });
});

describe("resolveLocale", () => {
  it("narrows to the shipped locale set", () => {
    expect(resolveLocale({ preference: "ar" })).toBe("ar");
    expect(resolveLocale({})).toBe("en");
  });
});

describe("readCookie", () => {
  it("reads one value out of a cookie header", () => {
    expect(readCookie("theme=dark; locale=de", LOCALE_COOKIE)).toBe("de");
  });

  it("decodes an encoded value and ignores a malformed pair", () => {
    expect(readCookie("broken; locale=de-AT", LOCALE_COOKIE)).toBe("de-AT");
    expect(readCookie("locale=de%2DAT", LOCALE_COOKIE)).toBe("de-AT");
  });

  it("returns nothing when the cookie is absent", () => {
    expect(readCookie("theme=dark", LOCALE_COOKIE)).toBeUndefined();
  });
});

describe("fromBrowser", () => {
  it("prefers the query parameter over the cookie", () => {
    const location = { search: "?lang=ar" } as Location;
    const doc = { cookie: "locale=de" } as Document;
    expect(fromBrowser(location, doc)).toBe("ar");
  });

  it("uses the cookie when no preference is in the query", () => {
    const location = { search: "" } as Location;
    const doc = { cookie: "locale=de" } as Document;
    expect(fromBrowser(location, doc)).toBe("de");
  });

  it("falls back to the languages the browser reports", () => {
    const location = { search: "" } as Location;
    const doc = { cookie: "" } as Document;
    Object.defineProperty(navigator, "languages", {
      value: ["ar-EG", "en"],
      configurable: true,
    });
    expect(fromBrowser(location, doc)).toBe("ar");
  });
});
