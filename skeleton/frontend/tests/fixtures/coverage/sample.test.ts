import { expect, test } from "vitest";

import { covered } from "./sample";

test("covers one of the two functions", () => {
  expect(covered()).toBe(1);
});
