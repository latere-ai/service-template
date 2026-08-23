import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "../src/app";
import { direction, isSupportedLocale, primarySubtag } from "../src/i18n/locales";

describe("direction", () => {
  it("follows the locale's script", () => {
    expect(direction("en")).toBe("ltr");
    expect(direction("de-AT")).toBe("ltr");
    expect(direction("ar")).toBe("rtl");
    expect(direction("he-IL")).toBe("rtl");
  });

  it("normalizes a tag before it reads the language", () => {
    expect(primarySubtag("AR_EG")).toBe("ar");
    expect(primarySubtag("")).toBe("");
  });

  it("knows which locales ship", () => {
    expect(isSupportedLocale("de")).toBe(true);
    expect(isSupportedLocale("fr")).toBe(false);
  });
});

describe("a right-to-left locale", () => {
  it("sets the document direction and language", () => {
    render(<App path="/" locale="ar" />);
    expect(document.documentElement.dir).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "خدمة تبدأ مكتملة",
    );
  });

  it("returns the document to the left-to-right locale", () => {
    render(<App path="/" locale="en" />);
    expect(document.documentElement.dir).toBe("ltr");
    expect(document.documentElement.lang).toBe("en");
  });
});

// Physical properties are correct in one writing direction and wrong in the
// other, so a stylesheet that used them would need a second stylesheet for a
// right-to-left locale. Logical properties need none.
describe("the stylesheet", () => {
  const physical = [
    "margin-left",
    "margin-right",
    "padding-left",
    "padding-right",
    "border-left",
    "border-right",
    "text-align: left",
    "text-align: right",
    "float: left",
    "float: right",
    "left:",
    "right:",
  ];

  function stylesheets(directory: string): string[] {
    return readdirSync(directory, { recursive: true, encoding: "utf8" })
      .filter((name) => name.endsWith(".css"))
      .map((name) => join(directory, name));
  }

  it("is the only stylesheet, so a locale cannot need a second one", () => {
    expect(stylesheets("src")).toEqual([join("src", "styles.css")]);
  });

  it.each(physical)("uses no %s", (property) => {
    const css = readFileSync(join("src", "styles.css"), "utf8");
    expect(css).not.toContain(property);
  });
});
