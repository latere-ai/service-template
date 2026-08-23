import { fileURLToPath } from "node:url";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

import { readThreshold } from "./tools/threshold";

const threshold = readThreshold(
  fileURLToPath(new URL("../.template.yaml", import.meta.url)),
);

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    // A hashed name changes when the content changes, which is what lets the
    // serving layer mark these responses immutable.
    rollupOptions: {
      output: {
        entryFileNames: "assets/[name]-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
      },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./tests/setup.ts"],
    include: ["tests/**/*.test.ts", "tests/**/*.test.tsx"],
    exclude: ["tests/fixtures/**", "node_modules/**", "dist/**"],
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
      include: ["src/**/*.ts", "src/**/*.tsx", "tools/*.ts", "prerender/*.ts"],
      // Entry points and command-line shells hold no branch of their own. They
      // are proved by the subprocess tests under tests/, which run them as
      // programs and therefore report no coverage.
      exclude: [
        "src/main.tsx",
        "src/i18n/messages/**",
        "tools/check-twins.ts",
        "tools/check-i18n.ts",
        "prerender/prerender.ts",
      ],
      thresholds: {
        statements: threshold,
        branches: threshold,
        functions: threshold,
        lines: threshold,
      },
    },
  },
});
