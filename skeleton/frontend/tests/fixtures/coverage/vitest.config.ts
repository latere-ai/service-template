import { defineConfig } from "vitest/config";

// The fixture the coverage gate is proved against: a module whose statements
// are half covered, gated at a threshold it cannot reach.
export default defineConfig({
  test: {
    environment: "node",
    include: ["sample.test.ts"],
    coverage: {
      provider: "v8",
      reporter: ["text"],
      include: ["sample.ts"],
      thresholds: { statements: 90, branches: 90, functions: 90, lines: 90 },
    },
  },
});
