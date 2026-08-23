// The locale set the interface ships with, the base locale every other locale
// is measured against, and the writing direction each one needs.

/** SupportedLocales is the ordered locale set. The order is the preference
 * order used when a request names a language without a region. */
export const SUPPORTED_LOCALES = ["en", "de", "ar"] as const;

export type Locale = (typeof SUPPORTED_LOCALES)[number];

/** BASE_LOCALE is the locale authors write first. The completeness gate
 * measures every other catalog against it. */
export const BASE_LOCALE: Locale = "en";

/** Direction is the writing direction of a locale, and the value of the
 * document `dir` attribute. */
export type Direction = "ltr" | "rtl";

// Languages written right to left. The list is by language subtag, because
// direction is a property of the script a language uses and not of the region.
const RTL_LANGUAGES = new Set([
  "ar",
  "arc",
  "dv",
  "fa",
  "ha",
  "he",
  "khw",
  "ks",
  "ps",
  "sd",
  "ur",
  "yi",
]);

/** primarySubtag returns the language part of a tag, lowercased. */
export function primarySubtag(tag: string): string {
  const normalized = tag.trim().toLowerCase().replace(/_/g, "-");
  const first = normalized.split("-")[0];
  return first ?? "";
}

/** direction returns the writing direction for a locale. Layout uses logical
 * properties, so a right-to-left locale needs this attribute and no second
 * stylesheet. */
export function direction(locale: string): Direction {
  return RTL_LANGUAGES.has(primarySubtag(locale)) ? "rtl" : "ltr";
}

/** isSupportedLocale reports whether a tag is one of the shipped locales. */
export function isSupportedLocale(tag: string): tag is Locale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(tag);
}
