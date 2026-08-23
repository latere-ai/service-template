import { describe, expect, it } from "vitest";

import {
  absolute,
  escapeAttribute,
  escapeJsonLd,
  escapeText,
  renderHead,
} from "../src/lib/metadata";
import type { Metadata } from "../src/lib/routes";
import { SCHEMA_CONTEXT } from "../src/lib/structured-data";

const metadata: Metadata = {
  title: "Pricing",
  description: 'One plan, "billed" per month',
  canonical: "/pricing",
  image: "/social-card.png",
  structuredData: {
    "@context": SCHEMA_CONTEXT,
    "@type": "WebPage",
    name: "Pricing",
    description: "One plan",
  },
};

const options = { origin: "https://example.com/", locale: "en", siteName: "Service" };

describe("escaping", () => {
  it("escapes the characters that would end an element early", () => {
    expect(escapeText("<a> & </a>")).toBe("&lt;a&gt; &amp; &lt;/a&gt;");
    expect(escapeAttribute('say "hi"')).toBe("say &quot;hi&quot;");
  });

  it("escapes a structured data block so it cannot close its own script", () => {
    const rendered = escapeJsonLd({ name: "</script><script>alert(1)</script>" });
    expect(rendered).not.toContain("</script>");
    expect(JSON.parse(rendered) as { name: string }).toEqual({
      name: "</script><script>alert(1)</script>",
    });
  });

  it("joins an origin and a path without doubling the separator", () => {
    expect(absolute("https://example.com/", "/docs")).toBe("https://example.com/docs");
  });
});

describe("renderHead", () => {
  const head = renderHead(metadata, options);

  it("carries the title and description a search client reads", () => {
    expect(head).toContain("<title>Pricing</title>");
    expect(head).toContain(
      '<meta name="description" content="One plan, &quot;billed&quot; per month" />',
    );
  });

  it("carries an absolute canonical URL", () => {
    expect(head).toContain(
      '<link rel="canonical" href="https://example.com/pricing" />',
    );
  });

  it("carries the social preview a link unfurler reads", () => {
    expect(head).toContain('<meta property="og:title" content="Pricing" />');
    expect(head).toContain(
      '<meta property="og:image" content="https://example.com/social-card.png" />',
    );
    expect(head).toContain(
      '<meta name="twitter:card" content="summary_large_image" />',
    );
    expect(head).toContain('<meta property="og:locale" content="en" />');
    expect(head).toContain('<meta property="og:site_name" content="Service" />');
  });

  it("carries the structured data an answer engine reads", () => {
    const block = /<script type="application\/ld\+json">([\s\S]*)<\/script>/.exec(head);
    expect(block?.[1]).toBeDefined();
    expect(JSON.parse(block![1]!) as Record<string, unknown>).toEqual(
      metadata.structuredData,
    );
  });
});
