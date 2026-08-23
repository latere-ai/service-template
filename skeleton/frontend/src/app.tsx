// The application tree. The same tree renders at build time and in the
// browser, so a prerendered document and the interactive page cannot describe
// different pages.

import { useCallback, useEffect, useState } from "react";

import { Layout } from "./components/layout";
import { NavigationContext } from "./components/link";
import { I18nProvider } from "./i18n/provider";
import type { Locale } from "./i18n/locales";
import { find, type Route } from "./lib/routes";
import { ROUTES } from "./routes/manifest";
import { NotFound } from "./routes/not-found";

export interface AppProps {
  /** path is the route being rendered. The build-time renderer passes the
   * route it is rendering; the browser passes the current location. */
  readonly path: string;
  readonly locale: Locale;
  /** navigate is absent at build time, where there is nowhere to navigate. */
  readonly navigate?: (to: string) => void;
  readonly routes?: readonly Route[];
}

export function App({ path, locale, navigate, routes = ROUTES }: AppProps) {
  const route = find(routes, path);
  const Component = route?.component ?? NotFound;
  return (
    <NavigationContext value={{ path, ...(navigate ? { navigate } : {}) }}>
      <I18nProvider locale={locale}>
        <Layout>
          <Component />
        </Layout>
      </I18nProvider>
    </NavigationContext>
  );
}

export interface BrowserAppProps {
  readonly locale: Locale;
  readonly initialPath: string;
  readonly history?: Pick<History, "pushState">;
}

/** BrowserApp adds client-side navigation to the tree. The router is a path
 * in state and a popstate listener, because the route manifest is a flat list
 * of exact paths and anything larger would be a dependency with no work to
 * do. */
export function BrowserApp({ locale, initialPath, history }: BrowserAppProps) {
  const [path, setPath] = useState(initialPath);
  useEffect(() => {
    const onPopState = () => {
      setPath(window.location.pathname);
    };
    window.addEventListener("popstate", onPopState);
    return () => {
      window.removeEventListener("popstate", onPopState);
    };
  }, []);
  const navigate = useCallback(
    (to: string) => {
      (history ?? window.history).pushState(null, "", to);
      setPath(to);
    },
    [history],
  );
  return <App path={path} locale={locale} navigate={navigate} />;
}
