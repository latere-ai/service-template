// A navigation link. It renders the same markup in the build-time renderer and
// in the browser, which is what lets a prerendered document hydrate without a
// mismatch. The click handler is the only difference, and a handler is not
// markup.

import { createContext, use, type ReactNode } from "react";

/** Navigation is what a link needs from the surrounding application: where it
 * is, and how to go somewhere else. A tree rendered at build time supplies the
 * path and no navigator, so a link there is an ordinary anchor. */
export interface Navigation {
  readonly path: string;
  readonly navigate?: (to: string) => void;
}

export const NavigationContext = createContext<Navigation>({ path: "/" });

export interface LinkProps {
  readonly to: string;
  readonly children: ReactNode;
}

/** Link navigates in the browser without a document load, and stays a plain
 * anchor for a client that runs no JavaScript. */
export function Link({ to, children }: LinkProps) {
  const navigation = use(NavigationContext);
  const current = navigation.path === to;
  return (
    <a
      href={to}
      {...(current ? { "aria-current": "page" as const } : {})}
      onClick={(event) => {
        const navigate = navigation.navigate;
        if (
          navigate === undefined ||
          event.defaultPrevented ||
          event.button !== 0 ||
          event.metaKey ||
          event.ctrlKey ||
          event.shiftKey ||
          event.altKey
        ) {
          return;
        }
        event.preventDefault();
        navigate(to);
      }}
    >
      {children}
    </a>
  );
}
