import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * E2E oncesi hazirlik: `npx playwright test` tek basina yeterli olsun.
 *
 *  1. 5 MB'lik test verisi uretilir (repoda tutulmuyor).
 *  2. Ajan binary'si derlenir (testler onu child process olarak baslatiyor).
 *
 * Web derlemesi BURADA DEGIL, webServer komutunda yapiliyor: Playwright
 * webServer'i globalSetup'tan once baslatiyor ve dist yoksa preview cikiyor.
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
    throw new Error(`${what} basarisiz (${cmd} ${args.join(" ")})`);
  }
}

export default function globalSetup(): void {
  run("go", ["run", "./internal/testsite/gen"], agentDir, "test verisi uretimi");
  run("go", ["build", "-o", BINARY, "."], agentDir, "ajan derlemesi");
}
