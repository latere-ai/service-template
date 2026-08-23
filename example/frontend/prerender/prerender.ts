// The command the build runs after the bundler. It reads the deployment
// configuration and holds no logic of its own.

import { join } from "node:path";

import { BASE_LOCALE } from "../src/i18n/locales";
import { ROUTES } from "../src/routes/manifest";
import { prerenderAll } from "./run";

const dist = join(import.meta.dirname, "..", "dist");

// The origin and the site name are deployment configuration. The placeholder
// origin is obviously wrong wherever it reaches production, which is the point
// of it.
const origin = process.env["SITE_ORIGIN"] ?? "https://example.com";
const siteName = process.env["SITE_NAME"] ?? "Service";

try {
  const written = await prerenderAll(ROUTES, {
    dist,
    origin,
    siteName,
    locale: BASE_LOCALE,
  });
  for (const file of written) {
    console.log(`wrote ${file}`);
  }
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
}
