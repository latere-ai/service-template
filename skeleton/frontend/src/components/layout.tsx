// The frame every route renders inside. It holds the navigation, the main
// landmark, and the footer, so a route component holds its own content only.

import type { ReactNode } from "react";

import { useTranslate } from "../i18n/provider";
import { Link } from "./link";

export interface LayoutProps {
  readonly children: ReactNode;
}

export function Layout({ children }: LayoutProps) {
  const t = useTranslate();
  return (
    <div className="layout">
      <nav className="nav" aria-label={t("nav.home")}>
        <Link to="/">{t("nav.home")}</Link>
        <Link to="/docs">{t("nav.docs")}</Link>
        <Link to="/pricing">{t("nav.pricing")}</Link>
        <Link to="/dashboard">{t("nav.dashboard")}</Link>
      </nav>
      <main className="content">{children}</main>
      <footer className="footer">
        {t("footer.copyright", {
          // A year is an identifier and not a quantity, so it is passed as
          // text and keeps its digits ungrouped.
          year: String(new Date().getUTCFullYear()),
          name: t("app.name"),
        })}
      </footer>
    </div>
  );
}
