// @vitest-environment node
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

// The first of the two defences against a compiled twin. The second is the
// check target, which is proved in the gates suite.
describe("the ignore list", () => {
  const ignores = readFileSync(".gitignore", "utf8");

  it.each(["src/**/*.js", "src/**/*.js.map"])("excludes %s", (pattern) => {
    expect(ignores).toContain(pattern);
  });

  it("excludes the dependency and build trees", () => {
    expect(ignores).toContain("node_modules/");
    expect(ignores).toContain("dist/");
    expect(ignores).toContain("coverage/");
  });

  it("carries the managed region markers, which the generator rewrites between", () => {
    expect(ignores).toContain("# >>> template: managed region, do not edit <<<");
    expect(ignores).toContain("# >>> template: end managed region <<<");
  });
});
