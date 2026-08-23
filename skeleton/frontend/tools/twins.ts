// The compiled-twin scan.
//
// A .js file beside its .ts source is preferred by the module resolver, so the
// test runner executes a stale build and reports a pass for code that is not
// the code under review. The symptom looks like a flaky test and the cause is
// invisible in the diff, which is why this is a gate and not a convention.

import { readdirSync } from "node:fs";
import { join, relative } from "node:path";

/** Twin is a compiled file and the source it shadows. */
export interface Twin {
  /** compiled is the path of the .js file, relative to the scanned root. */
  readonly compiled: string;
  /** source is the .ts or .tsx file it shadows. */
  readonly source: string;
}

const SOURCE_EXTENSIONS = [".ts", ".tsx"] as const;

function walk(root: string, directory: string, files: string[]): void {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const full = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name.startsWith(".")) {
        continue;
      }
      walk(root, full, files);
      continue;
    }
    files.push(relative(root, full));
  }
}

/** findTwins returns every compiled twin under a root, sorted by path so the
 * report is stable between runs. */
export function findTwins(root: string): Twin[] {
  const files: string[] = [];
  walk(root, root, files);
  const present = new Set(files);
  const twins: Twin[] = [];
  for (const file of files) {
    if (!file.endsWith(".js") && !file.endsWith(".js.map")) {
      continue;
    }
    const stem = file.replace(/\.js(\.map)?$/, "");
    for (const extension of SOURCE_EXTENSIONS) {
      if (present.has(stem + extension)) {
        twins.push({ compiled: file, source: stem + extension });
        break;
      }
    }
  }
  return twins.sort((a, b) => a.compiled.localeCompare(b.compiled));
}

/** report renders the failure. It names every path, because the fix is to
 * delete those files and a count would not say which. */
export function report(root: string, twins: readonly Twin[]): string {
  const lines = [
    `${twins.length} compiled ${twins.length === 1 ? "file shadows its" : "files shadow their"} source in ${root}:`,
  ];
  for (const twin of twins) {
    lines.push(`  ${join(root, twin.compiled)} shadows ${join(root, twin.source)}`);
  }
  lines.push(
    "delete them; a stale build is what the test runner would execute instead.",
  );
  return lines.join("\n");
}
