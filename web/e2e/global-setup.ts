import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * E2E oncesi hazirlik: `npx playwright test` tek basina yeterli olsun.
 *
 *  1. Web, e2e'nin servis ettigi alt yol (`/PeerPages/`) ile derlenir —
 *     yoksa index.html asset'leri "/assets/..." diye arar ve 404 alir.
 *  2. 5 MB'lik test verisi uretilir (repoda tutulmuyor).
 *  3. Ajan binary'si derlenir (testler onu child process olarak baslatiyor).
 */

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const agentDir = resolve(webDir, "..", "agent");
const BASE = "/PeerPages/";
const BINARY = process.platform === "win32" ? "peerpages.exe" : "peerpages";

function run(cmd: string, args: string[], cwd: string, what: string): void {
  const res = spawnSync(cmd, args, {
    cwd,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (res.status !== 0) {
    throw new Error(`${what} basarisiz (${cmd} ${args.join(" ")})`);
  }
}

export default function globalSetup(): void {
  run("node", ["scripts/build.mjs", BASE], webDir, "web derlemesi");
  run("go", ["run", "./internal/testsite/gen"], agentDir, "test verisi uretimi");
  run("go", ["build", "-o", BINARY, "."], agentDir, "ajan derlemesi");
}
