// @vitest-environment node
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { checkAll, format } from "../src/i18n/completeness";
import { loadDirectory } from "../tools/catalogs";

const fixture = join("tests", "fixtures", "i18n");

describe("loadDirectory", () => {
  it("returns the base locale first, whatever the file order is", () => {
    expect(loadDirectory(fixture, "en").map((c) => c.locale)).toEqual(["en", "de"]);
  });

  it("refuses a directory with no base locale, because a gate with nothing to compare against passes on anything", () => {
    expect(() => loadDirectory(fixture, "fr")).toThrow("base locale fr");
  });

  it("reads the catalogs this repository ships", () => {
    const shipped = loadDirectory(join("src", "i18n", "messages"), "en");
    expect(shipped.map((c) => c.locale)).toEqual(["en", "ar", "de"]);
    expect(checkAll(shipped).map(format)).toEqual([]);
  });
});
