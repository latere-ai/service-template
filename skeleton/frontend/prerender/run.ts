// The prerender step itself. Every public route is rendered through the same
// React tree that hydrates in the browser and written as a complete document,
// so a client that runs no JavaScript reads the content, the title, and the
// description in the first response.

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import { render } from "../src/entry-server";
import type { Locale } from "../src/i18n/locales";
import { publicRoutes, validate, type Route } from "../src/lib/routes";
import {
  buildRobots,
  buildSitemap,
  ROBOTS_NAME,
  SITEMAP_NAME,
} from "../src/lib/sitemap";
import { applyTemplate, isTemplate, outputPath } from "./document";

export interface Options {
  /** dist is the directory the bundler wrote and this step adds to. */
  readonly dist: string;
  /** origin is the absolute site origin the canonical, social, and sitemap
   * URLs are built from. */
  readonly origin: string;
  readonly siteName: string;
  readonly locale: Locale;
  readonly lastModified?: Date;
}

/** prerenderAll writes one document per public route, then the two documents
 * a crawler fetches by name. It returns the files it wrote, relative to the
 * output directory. */
export async function prerenderAll(
  routes: readonly Route[],
  options: Options,
): Promise<string[]> {
  const problems = validate(routes);
  if (problems.length > 0) {
    throw new Error(
      [
        "the route manifest is not fit to build:",
        ...problems.map((p) => `  ${p}`),
      ].join("\n"),
    );
  }

  const templatePath = join(options.dist, "index.html");
  const template = await readFile(templatePath, "utf8");
  // The landing route is written over the same document, because the serving
  // layer answers "/" with index.html and nothing else. A second run
  // therefore has no template to read, and says so rather than writing a
  // document with no content.
  if (!isTemplate(template)) {
    throw new Error(
      `${templatePath} is already prerendered; run the bundler first with: bun run build`,
    );
  }

  const written: string[] = [];
  for (const route of publicRoutes(routes)) {
    const result = render(route, {
      locale: options.locale,
      origin: options.origin,
      siteName: options.siteName,
    });
    const name = outputPath(route.path);
    const target = join(options.dist, name);
    await mkdir(dirname(target), { recursive: true });
    await writeFile(target, applyTemplate(template, result), "utf8");
    written.push(name);
  }

  const sitemap = buildSitemap(routes, {
    origin: options.origin,
    ...(options.lastModified === undefined
      ? {}
      : { lastModified: options.lastModified }),
  });
  await writeFile(join(options.dist, SITEMAP_NAME), sitemap, "utf8");
  await writeFile(
    join(options.dist, ROBOTS_NAME),
    buildRobots({ origin: options.origin }),
    "utf8",
  );
  written.push(SITEMAP_NAME, ROBOTS_NAME);
  return written;
}
