// Structured data validation. A preview fetcher or an answer engine reads the
// JSON-LD block and nothing else, so a block that omits a required property is
// a page those clients cannot describe. The build refuses it.

/** JsonLd is one structured data node. */
export type JsonLd = Readonly<Record<string, unknown>>;

/** SCHEMA_CONTEXT is the vocabulary every node declares. */
export const SCHEMA_CONTEXT = "https://schema.org";

// The properties each supported type must carry. The table is deliberately
// small: a type nobody on the public surface uses is a type nobody has to
// maintain a rule for.
const REQUIRED: Readonly<Record<string, readonly string[]>> = {
  WebSite: ["name", "url"],
  WebPage: ["name", "description"],
  Article: ["headline", "datePublished", "author"],
  Organization: ["name", "url"],
  Product: ["name", "offers"],
  Offer: ["price", "priceCurrency"],
  SoftwareApplication: ["name", "applicationCategory", "offers"],
  BreadcrumbList: ["itemListElement"],
  FAQPage: ["mainEntity"],
};

/** supportedTypes are the node types the validator knows. */
export function supportedTypes(): readonly string[] {
  return Object.keys(REQUIRED);
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** validate returns the problems in one structured data node, including the
 * nested nodes that declare a type of their own. The path names where the
 * problem is, because a node nested three deep is otherwise a search. */
export function validate(node: unknown, path = "structuredData"): string[] {
  if (!isObject(node)) {
    return [`${path} is not a JSON-LD object`];
  }
  const problems: string[] = [];
  if (path === "structuredData" && node["@context"] !== SCHEMA_CONTEXT) {
    problems.push(
      `${path} declares @context ${JSON.stringify(node["@context"])}, want "${SCHEMA_CONTEXT}"`,
    );
  }
  const type = node["@type"];
  if (typeof type !== "string" || type === "") {
    problems.push(`${path} declares no @type`);
    return problems;
  }
  const required = REQUIRED[type];
  if (required === undefined) {
    problems.push(
      `${path} declares @type ${type}, which the validator has no rule for; supported types are ${supportedTypes().join(", ")}`,
    );
    return problems;
  }
  for (const property of required) {
    const value = node[property];
    if (value === undefined || value === null || value === "") {
      problems.push(`${path} of type ${type} is missing "${property}"`);
    }
  }
  for (const [key, value] of Object.entries(node)) {
    if (key.startsWith("@")) {
      continue;
    }
    for (const [index, child] of (Array.isArray(value) ? value : [value]).entries()) {
      if (isObject(child) && typeof child["@type"] === "string") {
        const childPath = Array.isArray(value)
          ? `${path}.${key}[${index}]`
          : `${path}.${key}`;
        problems.push(...validate(child, childPath));
      }
    }
  }
  return problems;
}
