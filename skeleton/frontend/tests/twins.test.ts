import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { findTwins, report } from "../tools/twins";

const fixture = join("tests", "fixtures", "twins", "src");

describe("the compiled-twin scan", () => {
  const twins = findTwins(fixture);

  it("finds a compiled file beside its source, at any depth", () => {
    expect(twins).toEqual([
      { compiled: "a.js", source: "a.ts" },
      { compiled: join("nested", "b.js"), source: join("nested", "b.tsx") },
      { compiled: join("nested", "b.js.map"), source: join("nested", "b.tsx") },
    ]);
  });

  it("leaves a JavaScript file that shadows nothing alone", () => {
    expect(twins.map((t) => t.compiled)).not.toContain("standalone.js");
  });

  it("finds none in the source tree this repository ships", () => {
    expect(findTwins("src")).toEqual([]);
  });

  it("names every path, because the fix is to delete those files", () => {
    const message = report(fixture, twins);
    expect(message).toContain(join(fixture, "a.js"));
    expect(message).toContain(join(fixture, "a.ts"));
    expect(message).toContain(join(fixture, "nested", "b.js"));
    expect(message).toContain("3 compiled files shadow their source");
  });

  it("counts one twin in the singular", () => {
    expect(report("src", [{ compiled: "a.js", source: "a.ts" }])).toContain(
      "1 compiled file shadows its source",
    );
  });
});
