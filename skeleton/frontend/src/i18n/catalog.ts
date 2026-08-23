// The message catalog format, shared by the interface and by the server side.
// One file per locale, keyed by a dotted identifier, so a string is translated
// once and read by both.

import ar from "./messages/ar.json";
import de from "./messages/de.json";
import en from "./messages/en.json";
import { BASE_LOCALE, type Locale, primarySubtag } from "./locales";

/** PluralCategory is a plural selection form. The set a language actually
 * uses is decided by the platform, not by this list. */
export type PluralCategory = "zero" | "one" | "two" | "few" | "many" | "other";

/** PluralMessage selects a string by plural category. A language with six
 * forms carries six entries; a language with two carries two. */
export type PluralMessage = Partial<Record<PluralCategory, string>>;

/** Message is a single string, or a plural selection. */
export type Message = string | PluralMessage;

/** Catalog is one locale file. `identical` marks the keys a translator
 * deliberately left equal to the base locale, which is what separates a
 * finished translation from one that was never started. */
export interface Catalog {
  readonly locale: string;
  readonly identical: readonly string[];
  readonly messages: Readonly<Record<string, Message>>;
}

/** Params are the named placeholder values a message interpolates. `count`
 * additionally drives plural selection. */
export type Params = Readonly<Record<string, string | number>>;

const CATALOGS: Readonly<Record<Locale, Catalog>> = {
  en,
  de,
  ar,
};

/** catalog returns the catalog of a locale. */
export function catalog(locale: Locale): Catalog {
  return CATALOGS[locale];
}

/** catalogs returns every shipped catalog, base locale first. */
export function catalogs(): readonly Catalog[] {
  const base = CATALOGS[BASE_LOCALE];
  const rest = Object.values(CATALOGS).filter((c) => c !== base);
  return [base, ...rest];
}

/** PLACEHOLDER matches a named placeholder, for example {count}. */
export const PLACEHOLDER = /\{([a-zA-Z][a-zA-Z0-9_]*)\}/g;

/** placeholders returns the placeholder names a message uses. A plural
 * message contributes the union over its categories, because a category that
 * needs a different set is still one message to the caller. */
export function placeholders(message: Message): ReadonlySet<string> {
  const names = new Set<string>();
  const texts = typeof message === "string" ? [message] : Object.values(message);
  for (const text of texts) {
    for (const match of text.matchAll(PLACEHOLDER)) {
      const name = match[1];
      if (name !== undefined) {
        names.add(name);
      }
    }
  }
  return names;
}

/** pluralCategories returns the plural forms a language uses, in the order
 * the platform reports them. */
export function pluralCategories(locale: string): readonly PluralCategory[] {
  const rules = new Intl.PluralRules(locale);
  return rules.resolvedOptions().pluralCategories;
}

/** select returns the plural form for a count in a locale, falling back to
 * `other`, which every language has. */
export function select(locale: string, message: PluralMessage, count: number): string {
  const category = new Intl.PluralRules(locale).select(count);
  return message[category] ?? message.other ?? "";
}

/** interpolate replaces named placeholders with their values, formatting a
 * number with the locale's own digits and grouping. An unknown placeholder is
 * left in place, so a missing value is visible rather than silently empty. */
export function interpolate(locale: string, text: string, params: Params): string {
  return text.replace(PLACEHOLDER, (whole, name: string) => {
    const value = params[name];
    if (value === undefined) {
      return whole;
    }
    return typeof value === "number"
      ? new Intl.NumberFormat(locale).format(value)
      : value;
  });
}

/** Resolved is a message and the locale it is written in, which is not always
 * the locale that was asked for. */
export interface Resolved {
  readonly message: Message;
  readonly source: string;
}

/** resolveMessage picks the message for a key, falling back to the base
 * catalog. The source locale travels with it, because plural selection follows
 * the language the string is written in and a fallback string is written in
 * the base language. */
export function resolveMessage(
  local: Catalog,
  base: Catalog,
  key: string,
): Resolved | undefined {
  const own = local.messages[key];
  if (own !== undefined) {
    return { message: own, source: local.locale };
  }
  const fallback = base.messages[key];
  if (fallback !== undefined) {
    return { message: fallback, source: base.locale };
  }
  return undefined;
}

/** translate resolves one key in one locale. A key no catalog declares
 * returns the key itself, so a rendered screen never shows an empty region
 * where a string should be. */
export function translate(locale: Locale, key: string, params: Params = {}): string {
  const resolved = resolveMessage(catalog(locale), catalog(BASE_LOCALE), key);
  if (resolved === undefined) {
    return key;
  }
  const text =
    typeof resolved.message === "string"
      ? resolved.message
      : select(
          primarySubtag(resolved.source),
          resolved.message,
          Number(params["count"] ?? 0),
        );
  // Numbers are formatted in the reader's locale even when the string itself
  // came from the fallback, because the digits belong to the reader.
  return interpolate(locale, text, params);
}
