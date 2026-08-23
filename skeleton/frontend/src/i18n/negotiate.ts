// Language negotiation. One resolver, one order, used by the interface and by
// the server side, so a server-produced error and the interface around it
// never disagree about the language.

import { primarySubtag, SUPPORTED_LOCALES, BASE_LOCALE, type Locale } from "./locales";

/** LOCALE_COOKIE is the cookie both sides read and write. */
export const LOCALE_COOKIE = "locale";

/** Request is everything negotiation reads. Each field is a candidate source,
 * in precedence order: an explicit choice the user made, the cookie that
 * choice was stored in, and the header the browser sends. */
export interface Request {
  /** preference is an explicit choice, for example a query parameter or a
   * stored account setting. It wins because the user made it deliberately. */
  readonly preference?: string | null;
  readonly cookie?: string | null;
  readonly acceptLanguage?: string | null;
}

/** Options are the locale set to choose from. */
export interface Options {
  readonly supported?: readonly string[];
  readonly defaultLocale?: string;
}

interface Weighted {
  readonly tag: string;
  readonly quality: number;
  readonly order: number;
}

/** parseAcceptLanguage returns the tags of an Accept-Language header, highest
 * quality first, dropping the entries the client refused with q=0. Equal
 * qualities keep header order, which makes the result stable. */
export function parseAcceptLanguage(header: string): string[] {
  const entries: Weighted[] = [];
  header.split(",").forEach((part, order) => {
    const [rawTag, ...parameters] = part.split(";");
    const tag = rawTag?.trim() ?? "";
    if (tag === "") {
      return;
    }
    let quality = 1;
    for (const parameter of parameters) {
      const match = /^\s*q\s*=\s*([0-9.]+)\s*$/i.exec(parameter);
      if (match?.[1] !== undefined) {
        const parsed = Number(match[1]);
        quality = Number.isNaN(parsed) ? 0 : parsed;
      }
    }
    if (quality <= 0) {
      return;
    }
    entries.push({ tag, quality, order });
  });
  return entries
    .sort((a, b) =>
      b.quality === a.quality ? a.order - b.order : b.quality - a.quality,
    )
    .map((e) => e.tag);
}

/** match resolves one candidate tag against the supported set. An exact match
 * wins over a language match, and a language match takes the first supported
 * locale of that language, so the supported order is the tie-break. */
export function match(tag: string, supported: readonly string[]): string | undefined {
  const normalized = tag.trim().toLowerCase().replace(/_/g, "-");
  if (normalized === "" || normalized === "*") {
    return undefined;
  }
  const exact = supported.find((s) => s.toLowerCase() === normalized);
  if (exact !== undefined) {
    return exact;
  }
  const language = primarySubtag(normalized);
  return supported.find((s) => primarySubtag(s) === language);
}

/** resolve returns the locale for a request.
 *
 * The order is explicit preference, then cookie, then Accept-Language in
 * quality order, then the default locale. Every candidate is matched with the
 * rule above before the next source is consulted, so an unsupported explicit
 * preference falls through rather than failing. */
export function resolve(request: Request, options: Options = {}): string {
  const supported = options.supported ?? SUPPORTED_LOCALES;
  const fallback = options.defaultLocale ?? supported[0] ?? BASE_LOCALE;
  const candidates: string[] = [];
  if (request.preference != null) {
    candidates.push(request.preference);
  }
  if (request.cookie != null) {
    candidates.push(request.cookie);
  }
  if (request.acceptLanguage != null) {
    candidates.push(...parseAcceptLanguage(request.acceptLanguage));
  }
  for (const candidate of candidates) {
    const found = match(candidate, supported);
    if (found !== undefined) {
      return found;
    }
  }
  return fallback;
}

/** resolveLocale is resolve narrowed to the shipped locale set. */
export function resolveLocale(request: Request): Locale {
  return resolve(request, {
    supported: SUPPORTED_LOCALES,
    defaultLocale: BASE_LOCALE,
  }) as Locale;
}

/** readCookie returns one cookie value from a Cookie header or from
 * `document.cookie`, both of which use the same syntax. */
export function readCookie(header: string, name: string): string | undefined {
  for (const part of header.split(";")) {
    const index = part.indexOf("=");
    if (index < 0) {
      continue;
    }
    if (part.slice(0, index).trim() === name) {
      return decodeURIComponent(part.slice(index + 1).trim());
    }
  }
  return undefined;
}

/** fromBrowser resolves the locale of the current document: an explicit
 * choice in the query string, then the cookie, then the languages the browser
 * reports, which is what the Accept-Language header is built from. */
export function fromBrowser(location: Location, doc: Document): Locale {
  const preference = new URLSearchParams(location.search).get("lang");
  const cookie = readCookie(doc.cookie, LOCALE_COOKIE) ?? null;
  const languages = typeof navigator === "undefined" ? [] : navigator.languages;
  return resolveLocale({
    preference,
    cookie,
    acceptLanguage: languages.join(","),
  });
}
