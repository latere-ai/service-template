// noImplicitOverride: an override says so.
class Base {
  greet(): string {
    return "hello";
  }
}
export class Child extends Base {
  greet(): string {
    return "hi";
  }
}
