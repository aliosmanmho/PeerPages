import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * The preparation before e2e, so that `npx playwright test` is enough on
 * its own.
 *
 *  1. The 5 MB test data is generated (it is not kept in the repo).
 *  2. The agent binary is built (the tests start it as a child process).
 *
 * The web build happens NOT HERE but in the webServer command: Playwright
 * starts webServer before globalSetup, and with no dist the preview
 * exits.
 */

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const agentDir = resolve(webDir, "..", "agent");
const BINARY = process.platform === "win32" ? "peerpages.exe" : "peerpages";

function run(cmd: string, args: string[], cwd: string, what: string): void {
  const res = spawnSync(cmd, args, {
    cwd,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (res.status !== 0) {
    throw new Error(`${what} failed (${cmd} ${args.join(" ")})`);
  }
}

export default function globalSetup(): void {
  run("go", ["run", "./internal/testsite/gen"], agentDir, "generating the test data");
  run("go", ["build", "-o", BINARY, "."], agentDir, "building the agent");
}
