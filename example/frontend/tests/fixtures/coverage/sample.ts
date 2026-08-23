// Half of this module is exercised by the test beside it, so a run of this
// fixture lands below any threshold above fifty percent.
export function covered(): number {
  return 1;
}

export function uncovered(): number {
  const a = 2;
  const b = 3;
  return a + b;
}
