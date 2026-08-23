// Test setup. It adds the accessibility-oriented assertions, tells React that
// updates are wrapped in act, and clears the document between tests so a test
// never reads state another test left.

import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

afterEach(() => {
  cleanup();
});
