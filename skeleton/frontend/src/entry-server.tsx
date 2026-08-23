// The render entry point. One module with a narrow signature: a route and its
// data return the document body and the head elements.
//
// Keeping it isolated is what makes a later move to per-request rendering a
// change of where this function is called and not a change of what it does.

import { renderToString } from "react-dom/server";

import { App } from "./app";
import { direction, type Direction, type Locale } from "./i18n/locales";
import { renderHead } from "./lib/metadata";
import type { Route } from "./lib/routes";

/** RenderData is everything a document needs that the route does not carry. */
export interface RenderData {
  readonly locale: Locale;
  /** origin is the absolute site origin the canonical and social URLs are
   * built from. */
  readonly origin: string;
  readonly siteName: string;
}

/** RenderResult is one complete document, split into the parts the template
 * holds a slot for. */
export interface RenderResult {
  readonly html: string;
  readonly head: string;
  readonly lang: string;
  readonly dir: Direction;
  readonly path: string;
}

/** render produces the body markup and the head elements of one route.
 *
 * A public route without metadata is rejected here as well as by the manifest
 * validator, because this function is the last point at which the route name
 * is still known. */
export function render(route: Route, data: RenderData): RenderResult {
  if (route.surface === "public" && route.metadata === undefined) {
    throw new Error(`route "${route.path}" is public and declares no metadata`);
  }
  const html = renderToString(<App path={route.path} locale={data.locale} />);
  const head =
    route.metadata === undefined
      ? ""
      : renderHead(route.metadata, {
          origin: data.origin,
          locale: data.locale,
          siteName: data.siteName,
        });
  return {
    html,
    head,
    lang: data.locale,
    dir: direction(data.locale),
    path: route.path,
  };
}
