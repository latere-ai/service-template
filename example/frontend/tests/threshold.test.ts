// @vitest-environment node
//
// The configuration module pulls in the bundler, which needs the platform's
// own text encoder rather than the one the DOM environment substitutes.
import { describe, expect, it } from "vitest";

import viteConfig from "../vite.config";
import { DEFAULT_THRESHOLD, parseThreshold, readThreshold } from "../tools/threshold";

describe("the coverage threshold", () => {
  it("is read from the consumer declaration, which the Go gate reads too", () => {
    const declaration = [
      "template: github.com/latere-ai/service-template",
      "coverage:",
      "  threshold: 85",
      "  exclude:",
      "    - cmd/",
    ].join("\n");
    expect(parseThreshold(declaration)).toBe(85);
  });

  it("falls back to the documented default when nothing is declared", () => {
    expect(parseThreshold("template: x\n")).toBe(DEFAULT_THRESHOLD);
    expect(readThreshold("no/such/file.yaml")).toBe(DEFAULT_THRESHOLD);
  });

  it("ignores a threshold that belongs to another block", () => {
    expect(parseThreshold("other:\n  threshold: 10\n")).toBe(DEFAULT_THRESHOLD);
  });

  it("gates every counter of the test run at that number", () => {
    const coverage = viteConfig.test?.coverage;
    const thresholds =
      coverage !== undefined && "thresholds" in coverage
        ? coverage.thresholds
        : undefined;
    expect(thresholds).toMatchObject({
      statements: DEFAULT_THRESHOLD,
      branches: DEFAULT_THRESHOLD,
      functions: DEFAULT_THRESHOLD,
      lines: DEFAULT_THRESHOLD,
    });
  });
});
