// The compiled-twin guard. It runs before the test target, so a contaminated
// tree fails with the paths named instead of producing misleading results.

import { findTwins, report } from "./twins";

const root = process.argv[2] ?? "src";
const twins = findTwins(root);
if (twins.length > 0) {
  console.error(report(root, twins));
  process.exit(1);
}
console.log(`no compiled twins in ${root}`);
