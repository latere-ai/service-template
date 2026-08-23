// Assembling one static document from the built template and a rendered
// route. The template is the document the bundler produced, so the script and
// stylesheet references stay the ones the build hashed.

import { escapeAttribute } from "../src/lib/metadata";
import { PRERENDER_ATTRIBUTE } from "../src/lib/mount";

/** HEAD_SLOT and BODY_SLOT are the markers index.html carries. A template
 * that lost one would produce a document with no content and no error, so a
 * missing marker is a build failure. */
export const HEAD_SLOT = "<!--app-head-->";
export const BODY_SLOT = "<!--app-html-->";

export interface Slots {
  readonly head: string;
  readonly html: string;
  readonly lang: string;
  readonly dir: string;
  readonly path: string;
}

/** isTemplate reports whether a document still carries both slots. A
 * document the prerender already wrote does not, because the slots were
 * replaced by content. */
export function isTemplate(document: string): boolean {
  return document.includes(HEAD_SLOT) && document.includes(BODY_SLOT);
}

/** applyTemplate writes a rendered route into the built template. */
export function applyTemplate(template: string, slots: Slots): string {
  for (const marker of [HEAD_SLOT, BODY_SLOT]) {
    if (!template.includes(marker)) {
      throw new Error(`the built template holds no ${marker} marker`);
    }
  }
  return template
    .replace(
      /<html\b[^>]*>/i,
      `<html lang="${escapeAttribute(slots.lang)}" dir="${escapeAttribute(slots.dir)}">`,
    )
    .replace(HEAD_SLOT, slots.head)
    .replace(BODY_SLOT, slots.html)
    .replace(
      new RegExp(`${PRERENDER_ATTRIBUTE}="[^"]*"`),
      `${PRERENDER_ATTRIBUTE}="${escapeAttribute(slots.path)}"`,
    );
}

/** outputPath maps a route to the file the serving layer looks for. The
 * serving layer resolves "/docs" to "docs/index.html", so a directory per
 * route is what a deep link finds. */
export function outputPath(routePath: string): string {
  const trimmed = routePath.replace(/^\/+|\/+$/g, "");
  return trimmed === "" ? "index.html" : `${trimmed}/index.html`;
}
