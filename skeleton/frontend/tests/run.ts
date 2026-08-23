// Running a command the way a developer or CI runs it. A gate is proved by
// executing it and reading its exit code, because a gate asserted from the
// inside can still be wired up wrongly.

import { spawnSync } from "node:child_process";

export interface Run {
  readonly status: number;
  readonly output: string;
}

/** run executes a command in the frontend directory and joins both output
 * streams, which is where a tool writes its reason. */
export function run(command: string, args: readonly string[]): Run {
  const result = spawnSync(command, [...args], {
    encoding: "utf8",
    env: { ...process.env, FORCE_COLOR: "0", NO_COLOR: "1" },
  });
  return {
    status: result.status ?? 1,
    output: `${result.stdout ?? ""}${result.stderr ?? ""}`,
  };
}
