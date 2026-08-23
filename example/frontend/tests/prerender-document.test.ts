import { describe, expect, it } from "vitest";

import {
  applyTemplate,
  BODY_SLOT,
  HEAD_SLOT,
  isTemplate,
  outputPath,
} from "../prerender/document";
import { PRERENDER_ATTRIBUTE } from "../src/lib/mount";

const template = `<!doctype html>
<html lang="en" dir="ltr">
  <head>
    <meta charset="utf-8" />
    ${HEAD_SLOT}
  </head>
  <body>
    <div id="root" ${PRERENDER_ATTRIBUTE}="">${BODY_SLOT}</div>
  </body>
</html>
`;

const slots = {
  head: "<title>Docs</title>",
  html: "<main>Docs</main>",
  lang: "ar",
  dir: "rtl",
  path: "/docs",
};

describe("applyTemplate", () => {
  const document = applyTemplate(template, slots);

  it("writes the head, the body, and the document language", () => {
    expect(document).toContain("<title>Docs</title>");
    expect(document).toContain("<main>Docs</main>");
    expect(document).toContain('<html lang="ar" dir="rtl">');
  });

  it("stamps the path the markup was rendered for", () => {
    expect(document).toContain(`${PRERENDER_ATTRIBUTE}="/docs"`);
  });

  it("keeps the bundler's own references, which carry the content hashes", () => {
    expect(document).toContain('<meta charset="utf-8" />');
  });

  it.each([HEAD_SLOT, BODY_SLOT])("fails when the template lost %s", (marker) => {
    expect(() => applyTemplate(template.replace(marker, ""), slots)).toThrow(marker);
  });
});

describe("isTemplate", () => {
  it("separates the bundler's document from one the prerender already wrote", () => {
    expect(isTemplate(template)).toBe(true);
    expect(isTemplate(applyTemplate(template, slots))).toBe(false);
  });
});

describe("outputPath", () => {
  it.each([
    ["/", "index.html"],
    ["/docs", "docs/index.html"],
    ["/docs/", "docs/index.html"],
    ["/a/b", "a/b/index.html"],
  ])("maps %s to %s, which is what a deep link finds", (route, file) => {
    expect(outputPath(route)).toBe(file);
  });
});
