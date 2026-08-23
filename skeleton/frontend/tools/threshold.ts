// The coverage threshold has one declaration site: `.template.yaml` at the
// repository root, which the Go coverage gate reads as well. A frontend that
// restated the number would let the two halves of one repository drift apart
// without either half noticing.

import { readFileSync } from "node:fs";

/** DEFAULT_THRESHOLD matches the default the Go coverage gate documents, so a
 * repository that declares nothing is gated identically on both sides. */
export const DEFAULT_THRESHOLD = 90;

// The declaration is a two-level block. The pattern reads the `threshold`
// entry of the `coverage` block and ignores a `threshold` key that belongs to
// another block.
const THRESHOLD =
  /^coverage:[ \t]*(?:\r?\n[ \t]+[^\n]*)*?\r?\n[ \t]+threshold:[ \t]*(\d+)/m;

/** parseThreshold reads the declared threshold from the text of a
 * `.template.yaml`, or returns the default when it declares none. */
export function parseThreshold(text: string): number {
  const match = THRESHOLD.exec(text);
  if (match?.[1] === undefined) {
    return DEFAULT_THRESHOLD;
  }
  return Number(match[1]);
}

/** readThreshold reads the declaration at a path. A repository that holds no
 * declaration, which is the template's own repository, is gated at the
 * default. */
export function readThreshold(path: string): number {
  try {
    return parseThreshold(readFileSync(path, "utf8"));
  } catch {
    return DEFAULT_THRESHOLD;
  }
}
