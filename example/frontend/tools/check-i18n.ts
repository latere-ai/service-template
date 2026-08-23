// The completeness gate. A locale that falls behind the base locale shows the
// user a mix of languages, and nothing at runtime objects, so the objection is
// here.

import { checkAll, format } from "../src/i18n/completeness";
import { BASE_LOCALE } from "../src/i18n/locales";
import { loadDirectory } from "./catalogs";

const directory = process.argv[2] ?? "src/i18n/messages";
const base = process.argv[3] ?? BASE_LOCALE;

const catalogs = loadDirectory(directory, base);
const problems = checkAll(catalogs);
if (problems.length > 0) {
  console.error(`${problems.length} message problems in ${directory}:`);
  for (const problem of problems) {
    console.error(`  ${format(problem)}`);
  }
  process.exit(1);
}
console.log(`${catalogs.length} locales are complete against the base locale ${base}`);
