// noUncheckedIndexedAccess: an index read is possibly undefined.
export function first(items: string[]): string {
  const value = items[0];
  return value.toUpperCase();
}
