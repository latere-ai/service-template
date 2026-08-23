// Reading message catalogs from a directory. The completeness gate uses it so
// it can check any catalog set, including the fixtures that prove the gate
// still fails.

import { readFileSync, readdirSync } from "node:fs";
import { basename, join } from "node:path";

import type { Catalog } from "../src/i18n/catalog";

/** loadDirectory reads every catalog in a directory, base locale first. The
 * base locale must be present, because a gate with nothing to compare against
 * would pass on any input. */
export function loadDirectory(directory: string, baseLocale: string): Catalog[] {
  const catalogs = readdirSync(directory)
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => {
      const parsed = JSON.parse(readFileSync(join(directory, name), "utf8")) as Catalog;
      const locale = parsed.locale ?? basename(name, ".json");
      return { ...parsed, locale, identical: parsed.identical ?? [] };
    });
  const base = catalogs.find((c) => c.locale === baseLocale);
  if (base === undefined) {
    throw new Error(`${directory} holds no catalog for the base locale ${baseLocale}`);
  }
  return [base, ...catalogs.filter((c) => c !== base)];
}
