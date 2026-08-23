// strict: a nullable value is checked before it is used.
export function size(value: string | null): number {
  return value.length;
}
