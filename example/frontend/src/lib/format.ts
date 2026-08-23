// Formatting goes through the platform internationalization interfaces with
// the resolved locale. Manual formatting produces the wrong separator, the
// wrong digits, and the wrong order for at least one locale, and it does so
// silently.

/** UTC is the zone every stored and transmitted instant uses. A value that
 * changes zone between storage and display changes meaning. */
export const UTC = "UTC";

/** instant parses a transmitted timestamp. The input is UTC by contract, so a
 * string without a zone designator is read as UTC rather than as local time. */
export function instant(value: Date | string | number): Date {
  if (value instanceof Date) {
    return value;
  }
  if (typeof value === "number") {
    return new Date(value);
  }
  const hasZone = /(?:Z|[+-]\d{2}:?\d{2})$/i.test(value);
  return new Date(hasZone ? value : `${value}Z`);
}

/** formatDate renders an instant in the viewer's zone unless a zone is named,
 * which is what makes a stored UTC value read correctly wherever it is seen. */
export function formatDate(
  locale: string,
  value: Date | string | number,
  options: Intl.DateTimeFormatOptions = { dateStyle: "medium" },
): string {
  return new Intl.DateTimeFormat(locale, options).format(instant(value));
}

/** formatNumber renders a number with the locale's digits, grouping, and
 * decimal separator. */
export function formatNumber(
  locale: string,
  value: number,
  options: Intl.NumberFormatOptions = {},
): string {
  return new Intl.NumberFormat(locale, options).format(value);
}

/** formatCurrency renders an amount in the given currency. The currency is a
 * property of the price and never of the locale, so it is a parameter. */
export function formatCurrency(
  locale: string,
  value: number,
  currency: string,
  options: Intl.NumberFormatOptions = {},
): string {
  return new Intl.NumberFormat(locale, {
    ...options,
    style: "currency",
    currency,
  }).format(value);
}

/** formatRelative renders the distance between two instants in words, for
 * example "3 days ago". */
export function formatRelative(
  locale: string,
  value: Date | string | number,
  now: Date | string | number = new Date(),
): string {
  const units: readonly [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 365 * 24 * 60 * 60 * 1000],
    ["month", 30 * 24 * 60 * 60 * 1000],
    ["day", 24 * 60 * 60 * 1000],
    ["hour", 60 * 60 * 1000],
    ["minute", 60 * 1000],
    ["second", 1000],
  ];
  const delta = instant(value).getTime() - instant(now).getTime();
  const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  for (const [unit, span] of units) {
    if (Math.abs(delta) >= span) {
      return format.format(Math.trunc(delta / span), unit);
    }
  }
  return format.format(0, "second");
}
