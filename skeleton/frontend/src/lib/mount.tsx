// How the browser attaches to the document it received.
//
// The document served for an unknown path is the shell, and the shell is the
// prerendered landing page, because the serving layer answers every
// client-side route with the same file. Hydrating that markup on a different
// route would reconcile a landing page into a dashboard and warn about a
// mismatch on every element. The prerendered path is therefore stamped on the
// container, and the browser hydrates only when it is the path being shown.

import type { ReactElement } from "react";
import { createRoot, hydrateRoot, type Root } from "react-dom/client";

/** PRERENDER_ATTRIBUTE names the path the container's markup was rendered
 * for. An empty value means the container holds no rendered markup. */
export const PRERENDER_ATTRIBUTE = "data-prerender-path";

/** MountMode is the two ways a tree attaches: onto markup that already
 * describes this route, or into an emptied container. */
export type MountMode = "hydrate" | "fresh";

/** mountMode decides which one applies. */
export function mountMode(container: Element, path: string): MountMode {
  const rendered = container.getAttribute(PRERENDER_ATTRIBUTE);
  if (rendered === null || rendered === "" || container.innerHTML.trim() === "") {
    return "fresh";
  }
  return rendered === path ? "hydrate" : "fresh";
}

export interface MountResult {
  readonly mode: MountMode;
  readonly root: Root;
}

/** mount attaches a tree to a container. */
export function mount(
  container: HTMLElement,
  element: ReactElement,
  path: string,
): MountResult {
  const mode = mountMode(container, path);
  if (mode === "hydrate") {
    return { mode, root: hydrateRoot(container, element) };
  }
  // The markup describes a different route, so it is discarded rather than
  // reconciled. Discarding is cheap and reconciling is wrong.
  container.innerHTML = "";
  const root = createRoot(container);
  root.render(element);
  return { mode, root };
}
