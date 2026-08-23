// @vitest-environment node
//
// Every gate this project ships, executed as a program. A gate is only worth
// its configuration if it fails when the thing it guards is broken, so each
// one is run against a fixture that breaks it.

import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { run } from "./run";

const MINUTE = 60_000;

describe("the compiled-twin guard", () => {
  it("fails on a tree that holds a twin, and names the path", () => {
    const result = run("bun", [
      "run",
      "tools/check-twins.ts",
      join("tests", "fixtures", "twins", "src"),
    ]);
    expect(result.status).not.toBe(0);
    expect(result.output).toContain(join("tests", "fixtures", "twins", "src", "a.js"));
    expect(result.output).toContain(join("tests", "fixtures", "twins", "src", "a.ts"));
  });

  it("passes on the tree this repository ships", () => {
    const result = run("bun", ["run", "tools/check-twins.ts", "src"]);
    expect(result.output).toContain("no compiled twins");
    expect(result.status).toBe(0);
  });
});

describe("the completeness gate", () => {
  it("fails a catalog set that has fallen behind, naming the locale and the key", () => {
    const result = run("bun", [
      "run",
      "tools/check-i18n.ts",
      join("tests", "fixtures", "i18n"),
    ]);
    expect(result.status).not.toBe(0);
    expect(result.output).toContain("de: only.in.base: missing");
    expect(result.output).toContain("de: greeting.hello: placeholders are vorname");
    expect(result.output).toContain("de: extra.key");
    expect(result.output).toContain("de: app.name");
  });

  it("passes the catalogs this repository ships", () => {
    const result = run("bun", ["run", "tools/check-i18n.ts"]);
    expect(result.output).toContain("locales are complete");
    expect(result.status).toBe(0);
  });
});

describe("the type check", () => {
  const project = join("tests", "fixtures", "strict", "tsconfig.json");
  const files = [
    "strict-null.ts",
    "no-unchecked-index.ts",
    "exact-optional.ts",
    "implicit-override.ts",
    "allow-js.ts",
  ] as const;

  it(
    "rejects a violation of each enabled flag",
    () => {
      const result = run("bun", ["x", "tsc", "--noEmit", "-p", project]);
      expect(result.status).not.toBe(0);
      for (const file of files) {
        expect(result.output).toContain(file);
      }
    },
    MINUTE,
  );

  // Turning one flag off must make exactly that fixture compile. Without this
  // control, a single unrelated error would make the run above pass while the
  // flags did nothing.
  it.each([
    [
      "noUncheckedIndexedAccess",
      ["--noUncheckedIndexedAccess", "false"],
      "no-unchecked-index.ts",
    ],
    [
      "exactOptionalPropertyTypes",
      ["--exactOptionalPropertyTypes", "false"],
      "exact-optional.ts",
    ],
    ["noImplicitOverride", ["--noImplicitOverride", "false"], "implicit-override.ts"],
    ["allowJs", ["--allowJs", "true"], "allow-js.ts"],
    [
      "strict",
      [
        "--strict",
        "false",
        "--exactOptionalPropertyTypes",
        "false",
        "--noUncheckedIndexedAccess",
        "false",
      ],
      "strict-null.ts",
    ],
  ] as const)(
    "reports %s and nothing else for its fixture",
    (_flag, off, file) => {
      const result = run("bun", ["x", "tsc", "--noEmit", "-p", project, ...off]);
      expect(result.output).not.toContain(file);
    },
    MINUTE,
  );

  it(
    "passes the source this repository ships",
    () => {
      const result = run("bun", ["x", "tsc", "--noEmit"]);
      expect(result.output).toBe("");
      expect(result.status).toBe(0);
    },
    MINUTE,
  );
});

describe("the coverage gate", () => {
  it(
    "fails a run that lands below the threshold",
    () => {
      const result = run("bun", [
        "x",
        "vitest",
        "run",
        "--coverage",
        "--root",
        join("tests", "fixtures", "coverage"),
      ]);
      expect(result.status).not.toBe(0);
      expect(result.output).toContain("does not meet global threshold");
    },
    2 * MINUTE,
  );
});

describe("the frozen install", () => {
  it(
    "refuses a lockfile that does not match the manifest, rather than resolving a different tree",
    () => {
      const directory = mkdtempSync(join(tmpdir(), "frontend-lockfile-"));
      cpSync("bun.lock", join(directory, "bun.lock"));
      const manifest = JSON.parse(readFileSync("package.json", "utf8")) as {
        dependencies: Record<string, string>;
      };
      manifest.dependencies["left-pad"] = "1.3.0";
      writeFileSync(join(directory, "package.json"), JSON.stringify(manifest, null, 2));

      // The mismatch is decided from the two files alone, so the check needs
      // no registry and fails the same way on a machine with no network.
      const result = run("bun", ["install", "--cwd", directory, "--frozen-lockfile"]);
      expect(result.status).not.toBe(0);
      expect(result.output).toMatch(/lockfile/i);
    },
    2 * MINUTE,
  );
});
