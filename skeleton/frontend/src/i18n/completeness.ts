// The completeness gate. A key added to the base locale and forgotten
// elsewhere is the recurring internationalization defect, because nothing at
// runtime objects: the interface simply shows a mix of languages.

import { type Catalog, type Message, placeholders, pluralCategories } from "./catalog";

/** Problem is one gate failure. It names the locale and the key, so the fix
 * is the message itself rather than a search. */
export interface Problem {
  readonly locale: string;
  readonly key: string;
  readonly reason: string;
}

/** format renders a problem as one line. */
export function format(p: Problem): string {
  return `${p.locale}: ${p.key}: ${p.reason}`;
}

function sortedNames(names: Iterable<string>): string {
  return [...names].sort().join(", ") || "none";
}

function equalMessage(a: Message, b: Message): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** check compares one catalog against the base catalog.
 *
 * It fails on a missing key, an extra key, a placeholder set that differs from
 * the base, a plural message that omits a form the language uses, and an
 * untranslated string. The last one is the reason the format carries an
 * `identical` list: a translator who decided a string is the same in both
 * languages says so, and a key nobody has touched yet is then distinguishable
 * from one that is deliberately equal. */
export function check(base: Catalog, other: Catalog): Problem[] {
  const problems: Problem[] = [];
  const marked = new Set(other.identical);

  for (const [key, baseMessage] of Object.entries(base.messages)) {
    const message = other.messages[key];
    if (message === undefined) {
      problems.push({
        locale: other.locale,
        key,
        reason: `missing, the base locale ${base.locale} declares it`,
      });
      continue;
    }

    const want = placeholders(baseMessage);
    const got = placeholders(message);
    if (sortedNames(want) !== sortedNames(got)) {
      problems.push({
        locale: other.locale,
        key,
        reason: `placeholders are ${sortedNames(got)}, the base locale uses ${sortedNames(want)}`,
      });
    }

    if (typeof baseMessage !== typeof message) {
      problems.push({
        locale: other.locale,
        key,
        reason:
          typeof message === "string"
            ? "is a single string, the base locale declares plural forms"
            : "declares plural forms, the base locale is a single string",
      });
    } else if (typeof message !== "string") {
      for (const category of pluralCategories(other.locale)) {
        if (message[category] === undefined) {
          problems.push({
            locale: other.locale,
            key,
            reason: `plural form "${category}" is missing, ${other.locale} uses it`,
          });
        }
      }
    }

    const identical = equalMessage(baseMessage, message);
    if (identical && !marked.has(key)) {
      problems.push({
        locale: other.locale,
        key,
        reason: `is identical to the base locale and is not listed in "identical", so it reads as untranslated`,
      });
    }
    if (!identical && marked.has(key)) {
      problems.push({
        locale: other.locale,
        key,
        reason: `is listed in "identical" but differs from the base locale`,
      });
    }
  }

  for (const key of Object.keys(other.messages)) {
    if (base.messages[key] === undefined) {
      problems.push({
        locale: other.locale,
        key,
        reason: `is not in the base locale ${base.locale}`,
      });
    }
  }

  for (const key of other.identical) {
    if (other.messages[key] === undefined) {
      problems.push({
        locale: other.locale,
        key,
        reason: `is listed in "identical" but the catalog does not declare it`,
      });
    }
  }

  return problems;
}

/** checkAll compares every catalog after the first against the first. */
export function checkAll(all: readonly Catalog[]): Problem[] {
  const [base, ...rest] = all;
  if (base === undefined) {
    return [];
  }
  return rest.flatMap((c) => check(base, c));
}
