#!/usr/bin/env node
/**
 * The web build: the app shell plus the hand-built Service Worker.
 *
 * Why a script: `base` has to be passed through an environment variable,
 * and there is no clean platform-independent way to do that in an npm
 * script (`VAR=x cmd` does not work on Windows, and Git Bash turns
 * `/PeerPages/` into a Windows path). Putting Node in between ends both
 * problems.
 *
 * Usage:
 *   node scripts/build.mjs             # base "/"
 *   node scripts/build.mjs /PeerPages/ # the GitHub Pages subpath
 */

import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const base = process.argv[2] ?? process.env.PEERPAGES_BASE ?? "/";

if (/^[A-Za-z]:/.test(base)) {
  // Git Bash (MSYS) turned the "/PeerPages/" argument into a Windows
  // path.
  console.error(
    `base was turned into a Windows path: ${base}
` +
      "If you are using Git Bash: MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/",
  );
  process.exit(1);
}
if (!base.startsWith("/") || !base.endsWith("/")) {
  console.error(`base has to start and end with "/", got: ${base}`);
  process.exit(1);
}

const env = { ...process.env, PEERPAGES_BASE: base };

function run(args) {
  const res = spawnSync("npx", args, {
    cwd: webDir,
    env,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (res.status !== 0) {
    console.error(`failed: npx ${args.join(" ")}`);
    process.exit(res.status ?? 1);
  }
}

// THE ORDER MATTERS: the later builds run with emptyOutDir: false, so
// they have to come after the app shell rather than wipe it.
run(["vite", "build"]);
run(["vite", "build", "--config", "vite.sw.config.ts"]);
run(["vite", "build", "--config", "vite.shim.config.ts"]);

console.log(`the web app was built (base=${base})`);
