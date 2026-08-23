// The route manifest. It is the one declaration of which routes exist and
// which of them a crawler may see.
//
// A public route is rendered to a complete document at build time and must
// declare its metadata. An application route ships the application shell and
// declares none, so nothing behind authentication can reach the static output.

import type { Route } from "../lib/routes";
import { SCHEMA_CONTEXT } from "../lib/structured-data";
import { Dashboard } from "./dashboard";
import { Docs } from "./docs";
import { Home } from "./home";
import { Pricing } from "./pricing";

const SITE_NAME = "Service";

export const ROUTES: readonly Route[] = [
  {
    path: "/",
    surface: "public",
    component: Home,
    metadata: {
      title: "Service",
      description:
        "A service that starts complete: configuration, observability, health, and a build pipeline are already wired.",
      canonical: "/",
      image: "/social-card.png",
      changeFrequency: "weekly",
      priority: 1,
      structuredData: {
        "@context": SCHEMA_CONTEXT,
        "@type": "WebSite",
        name: SITE_NAME,
        url: "/",
      },
    },
  },
  {
    path: "/docs",
    surface: "public",
    component: Docs,
    metadata: {
      title: "Documentation",
      description: "Every gate this repository runs, and what each one refuses.",
      canonical: "/docs",
      image: "/social-card.png",
      changeFrequency: "weekly",
      priority: 0.8,
      structuredData: {
        "@context": SCHEMA_CONTEXT,
        "@type": "WebPage",
        name: "Documentation",
        description: "Every gate this repository runs, and what each one refuses.",
      },
    },
  },
  {
    path: "/pricing",
    surface: "public",
    component: Pricing,
    metadata: {
      title: "Pricing",
      description: "One plan, billed per month.",
      canonical: "/pricing",
      image: "/social-card.png",
      changeFrequency: "monthly",
      priority: 0.5,
      structuredData: {
        "@context": SCHEMA_CONTEXT,
        "@type": "Product",
        name: SITE_NAME,
        offers: {
          "@type": "Offer",
          price: "49.00",
          priceCurrency: "EUR",
        },
      },
    },
  },
  {
    path: "/dashboard",
    surface: "app",
    component: Dashboard,
  },
];
