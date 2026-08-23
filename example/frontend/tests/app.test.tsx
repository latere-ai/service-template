import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { App, BrowserApp } from "../src/app";

describe("the application tree", () => {
  it("renders the route the manifest holds for a path", () => {
    render(<App path="/pricing" locale="en" />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Pricing");
    expect(screen.getByText("€49.00")).toBeInTheDocument();
  });

  it("renders a not-found page for a path the manifest does not hold", () => {
    render(<App path="/nowhere" locale="en" />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Not found");
  });

  it("marks the current link, which is what a screen reader announces", () => {
    render(<App path="/docs" locale="en" />);
    expect(screen.getByRole("link", { name: "Documentation" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("link", { name: "Home" })).not.toHaveAttribute(
      "aria-current",
    );
  });

  it("renders every string through the catalog of the surrounding locale", () => {
    render(<App path="/docs" locale="de" />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "Dokumentation",
    );
    expect(screen.getByRole("link", { name: "Start" })).toBeInTheDocument();
  });
});

describe("client-side navigation", () => {
  it("changes route without a document load and records the history entry", () => {
    const history = { pushState: vi.fn() };
    render(<BrowserApp locale="en" initialPath="/" history={history} />);
    act(() => {
      screen.getByRole("link", { name: "Pricing" }).click();
    });
    expect(history.pushState).toHaveBeenCalledWith(null, "", "/pricing");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Pricing");
  });

  it("follows a back navigation", () => {
    render(<BrowserApp locale="en" initialPath="/" />);
    window.history.pushState(null, "", "/docs");
    act(() => {
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "Documentation",
    );
    window.history.pushState(null, "", "/");
  });

  it("leaves a modified click to the browser, so a new tab still opens", () => {
    const history = { pushState: vi.fn() };
    render(<BrowserApp locale="en" initialPath="/" history={history} />);
    const link = screen.getByRole("link", { name: "Pricing" });
    act(() => {
      link.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true, metaKey: true }),
      );
    });
    expect(history.pushState).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "A service that starts complete",
    );
  });

  it("is an ordinary anchor where there is nothing to navigate with", () => {
    render(<App path="/" locale="en" />);
    const link = screen.getByRole("link", { name: "Pricing" });
    let prevented: boolean | undefined;
    // The listener runs after the tree's own handler and stops the document
    // load the anchor would otherwise start, which the test environment
    // cannot perform.
    document.addEventListener(
      "click",
      (event) => {
        prevented = event.defaultPrevented;
        event.preventDefault();
      },
      { once: true },
    );
    act(() => {
      link.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(prevented).toBe(false);
  });
});
