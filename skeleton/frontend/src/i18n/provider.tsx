// The locale a rendered tree reads, and the document attributes that follow
// from it. The same provider runs in the browser and in the build-time
// renderer, so a page cannot hydrate into a different language than it was
// rendered in.

import { createContext, useContext, useEffect, type ReactNode } from "react";

import { translate, type Params } from "./catalog";
import { BASE_LOCALE, direction, type Locale } from "./locales";

const LocaleContext = createContext<Locale>(BASE_LOCALE);

export interface I18nProviderProps {
  readonly locale: Locale;
  readonly children: ReactNode;
}

/** I18nProvider publishes the resolved locale to the tree and keeps the
 * document language and direction in step with it. */
export function I18nProvider({ locale, children }: I18nProviderProps) {
  useEffect(() => {
    const root = document.documentElement;
    root.lang = locale;
    root.dir = direction(locale);
  }, [locale]);
  return <LocaleContext value={locale}>{children}</LocaleContext>;
}

/** useLocale returns the locale of the surrounding tree. */
export function useLocale(): Locale {
  return useContext(LocaleContext);
}

/** Translate resolves a message key in the surrounding locale. */
export type Translate = (key: string, params?: Params) => string;

/** useTranslate returns the message resolver for the surrounding locale. */
export function useTranslate(): Translate {
  const locale = useLocale();
  return (key: string, params: Params = {}) => translate(locale, key, params);
}
