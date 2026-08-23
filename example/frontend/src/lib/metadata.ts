// The head elements a prerendered document carries. They are produced as text
// rather than as React elements, because the head of a static document is
// written once at build time and never reconciled.

import type { Metadata } from "./routes";

/** escapeText escapes the characters that would end an element early. */
export function escapeText(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

/** escapeAttribute escapes a value used inside a double-quoted attribute. */
export function escapeAttribute(value: string): string {
  return escapeText(value).replace(/"/g, "&quot;");
}

/** absolute joins an origin and an absolute path. */
export function absolute(origin: string, path: string): string {
  return `${origin.replace(/\/+$/, "")}${path}`;
}

/** escapeJsonLd renders a structured data node for embedding in a script
 * element. The sequence that would close the element early is escaped rather
 * than removed, because the value stays valid JSON either way. */
export function escapeJsonLd(node: unknown): string {
  return JSON.stringify(node, null, 2)
    .replace(/</g, "\\u003c")
    .replace(/>/g, "\\u003e")
    .replace(/&/g, "\\u0026");
}

export interface HeadOptions {
  readonly origin: string;
  readonly locale: string;
  readonly siteName: string;
}

/** renderHead returns the head elements of one public document: the title and
 * description a search client reads, the canonical URL that collapses
 * duplicates, the social preview a link unfurler reads, and the structured
 * data an answer engine reads. */
export function renderHead(metadata: Metadata, options: HeadOptions): string {
  const canonical = absolute(options.origin, metadata.canonical);
  const image = absolute(options.origin, metadata.image);
  const lines = [
    `<title>${escapeText(metadata.title)}</title>`,
    `<meta name="description" content="${escapeAttribute(metadata.description)}" />`,
    `<link rel="canonical" href="${escapeAttribute(canonical)}" />`,
    `<meta property="og:type" content="website" />`,
    `<meta property="og:site_name" content="${escapeAttribute(options.siteName)}" />`,
    `<meta property="og:locale" content="${escapeAttribute(options.locale)}" />`,
    `<meta property="og:title" content="${escapeAttribute(metadata.title)}" />`,
    `<meta property="og:description" content="${escapeAttribute(metadata.description)}" />`,
    `<meta property="og:url" content="${escapeAttribute(canonical)}" />`,
    `<meta property="og:image" content="${escapeAttribute(image)}" />`,
    `<meta name="twitter:card" content="summary_large_image" />`,
    `<meta name="twitter:title" content="${escapeAttribute(metadata.title)}" />`,
    `<meta name="twitter:description" content="${escapeAttribute(metadata.description)}" />`,
    `<meta name="twitter:image" content="${escapeAttribute(image)}" />`,
    `<script type="application/ld+json">${escapeJsonLd(metadata.structuredData)}</script>`,
  ];
  return lines.join("\n    ");
}
