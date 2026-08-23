import { describe, expect, it } from "vitest";

import {
  formatCurrency,
  formatDate,
  formatNumber,
  formatRelative,
  instant,
  UTC,
} from "../src/lib/format";

describe("instant", () => {
  it("reads a timestamp without a zone as UTC, which is the transport contract", () => {
    expect(instant("2026-03-01T12:00:00").toISOString()).toBe(
      "2026-03-01T12:00:00.000Z",
    );
  });

  it("keeps an explicit zone", () => {
    expect(instant("2026-03-01T12:00:00+02:00").toISOString()).toBe(
      "2026-03-01T10:00:00.000Z",
    );
  });

  it("accepts a date and an epoch value unchanged", () => {
    const date = new Date("2026-03-01T00:00:00Z");
    expect(instant(date)).toBe(date);
    expect(instant(date.getTime()).toISOString()).toBe("2026-03-01T00:00:00.000Z");
  });
});

describe("formatDate", () => {
  it("renders a stored UTC value in the zone it is asked for", () => {
    const value = "2026-03-01T23:30:00Z";
    expect(formatDate("en", value, { timeZone: UTC, dateStyle: "short" })).toBe(
      "3/1/26",
    );
    expect(
      formatDate("en", value, { timeZone: "Asia/Tokyo", dateStyle: "short" }),
    ).toBe("3/2/26");
  });

  it("follows the locale's own order", () => {
    const options: Intl.DateTimeFormatOptions = { timeZone: UTC, dateStyle: "short" };
    expect(formatDate("de", "2026-03-01T00:00:00Z", options)).toBe("01.03.26");
  });
});

describe("formatNumber and formatCurrency", () => {
  it("uses the locale's separators", () => {
    expect(formatNumber("en", 1234.5)).toBe("1,234.5");
    expect(formatNumber("de", 1234.5)).toBe("1.234,5");
  });

  it("renders a currency the locale does not belong to", () => {
    expect(formatCurrency("en", 49, "EUR")).toBe("€49.00");
    expect(formatCurrency("de", 49, "EUR")).toContain("49,00");
  });

  it("passes further options through", () => {
    expect(formatNumber("en", 0.25, { style: "percent" })).toBe("25%");
    expect(formatCurrency("en", 49, "EUR", { maximumFractionDigits: 0 })).toBe("€49");
  });
});

describe("formatRelative", () => {
  const now = "2026-03-10T00:00:00Z";

  it.each([
    ["2025-03-10T00:00:00Z", "last year"],
    ["2026-03-07T00:00:00Z", "3 days ago"],
    ["2026-03-10T02:00:00Z", "in 2 hours"],
    ["2026-03-10T00:00:30Z", "in 30 seconds"],
  ])("renders %s as %s", (value, want) => {
    expect(formatRelative("en", value, now)).toBe(want);
  });

  it("renders a distance below one second as now", () => {
    expect(formatRelative("en", now, now)).toBe("now");
  });
});
