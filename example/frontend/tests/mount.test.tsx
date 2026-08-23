import { act } from "react";
import { hydrateRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "../src/app";
import { render as renderRoute } from "../src/entry-server";
import { find } from "../src/lib/routes";
import { mount, mountMode, PRERENDER_ATTRIBUTE } from "../src/lib/mount";
import { ROUTES } from "../src/routes/manifest";

const data = {
  locale: "en",
  origin: "https://example.com",
  siteName: "Service",
} as const;

/** container builds the document the browser would have received: the markup
 * the build rendered for one route, stamped with that route's path. */
function container(renderedPath: string): HTMLElement {
  const element = document.createElement("div");
  element.id = "root";
  element.setAttribute(PRERENDER_ATTRIBUTE, renderedPath);
  element.innerHTML = renderRoute(find(ROUTES, renderedPath)!, data).html;
  document.body.append(element);
  return element;
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("mountMode", () => {
  it("hydrates markup rendered for the path being shown", () => {
    expect(mountMode(container("/docs"), "/docs")).toBe("hydrate");
  });

  it("mounts fresh when the markup was rendered for another route", () => {
    expect(mountMode(container("/"), "/dashboard")).toBe("fresh");
  });

  it("mounts fresh when the container holds no rendered markup", () => {
    const element = document.createElement("div");
    element.setAttribute(PRERENDER_ATTRIBUTE, "");
    expect(mountMode(element, "/")).toBe("fresh");
    element.setAttribute(PRERENDER_ATTRIBUTE, "/");
    expect(mountMode(element, "/")).toBe("fresh");
    element.removeAttribute(PRERENDER_ATTRIBUTE);
    element.innerHTML = "<p>stale</p>";
    expect(mountMode(element, "/")).toBe("fresh");
  });
});

describe("mount", () => {
  it("hydrates prerendered markup with no mismatch warning", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const element = container("/docs");
    let result: ReturnType<typeof mount> | undefined;
    await act(async () => {
      result = mount(element, <App path="/docs" locale="en" />, "/docs");
    });
    expect(result?.mode).toBe("hydrate");
    expect(errors).not.toHaveBeenCalled();
    expect(element.querySelector("h1")?.textContent).toBe("Documentation");
    act(() => {
      result?.root.unmount();
    });
  });

  // The serving layer answers every unknown path with one document, so the
  // markup a client-side route receives describes the landing page. Hydrating
  // it would reconcile a landing page into a dashboard, which is the warning
  // this branch exists to avoid.
  it("mounts fresh on a client-side route, with no mismatch warning", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const element = container("/");
    let result: ReturnType<typeof mount> | undefined;
    await act(async () => {
      result = mount(element, <App path="/dashboard" locale="en" />, "/dashboard");
    });
    expect(result?.mode).toBe("fresh");
    expect(errors).not.toHaveBeenCalled();
    expect(element.querySelector("h1")?.textContent).toBe("Dashboard");
    act(() => {
      result?.root.unmount();
    });
  });

  // The guard is only worth its code if the failure it prevents is real. This
  // is that failure, produced deliberately.
  it("would fail if the same markup were hydrated for another route", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const element = container("/");
    const recovered: unknown[] = [];
    let root: ReturnType<typeof hydrateRoot> | undefined;
    await act(async () => {
      root = hydrateRoot(element, <App path="/dashboard" locale="en" />, {
        onRecoverableError: (error) => {
          recovered.push(error);
        },
      });
    });
    expect(recovered.map(String).join(" ")).toMatch(/hydrat/i);
    act(() => {
      root?.unmount();
    });
  });
});
