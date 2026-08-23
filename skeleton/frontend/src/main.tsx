// The browser entry point. It resolves the locale, decides how to attach to
// the document it received, and holds no logic of its own.

import { BrowserApp } from "./app";
import { fromBrowser } from "./i18n/negotiate";
import { mount } from "./lib/mount";
import "./styles.css";

const container = document.getElementById("root");
if (container === null) {
  throw new Error("the document holds no #root container");
}

const locale = fromBrowser(window.location, document);
const path = window.location.pathname;
mount(container, <BrowserApp locale={locale} initialPath={path} />, path);
