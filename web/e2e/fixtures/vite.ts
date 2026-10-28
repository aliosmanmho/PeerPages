/**
 * The Vite dev server fixture (phase 6).
 *
 * The real half of the acceptance criterion: "Vite HMR works over the
 * tunnel". A fake ws endpoint does not prove that — HMR's own handshake
 * (the `vite-hmr` subprotocol, the token, the module update messages) has
 * to be measured against the real server. `vite` is already a
 * devDependency, so no new dependency is needed.
 */

import { type ChildProcess, spawn } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { LineWatcher } from "./agent";

const WEB_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const APP_DIR = join(WEB_DIR, "e2e", "fixtures", "vite-app");
const MESSAGE_FILE = join(APP_DIR, "src", "message.js");

export interface ViteApp {
  /** The target to hand to the agent (INCLUDING the base prefix). */
  targetURL: string;
  /** Changes `src/message.js`; HMR has to catch that. */
  setMessage: (text: string) => void;
  /** Puts the file back to its original state. */
  reset: () => void;
  output: () => string[];
  stop: () => void;
  process: ChildProcess;
}

export interface ViteAppOptions {
  /** The app shell's scope root; the fixture's `base` has to be the SAME. */
  readonly base: string;
}

/**
 * Starts the fixture project with `vite dev`.
 *
 * Why `base` matters: the Service Worker strips the scope prefix before
 * sending to the agent, while Vite expects prefixed paths. To line the
 * two up, the fixture's base is the scope root and the agent's target is
 * a URL carrying the same prefix. That also keeps the ROOT-ABSOLUTE paths
 * Vite produces inside the scope (the structural boundary in BRIEF
 * section 8).
 */
export async function startViteApp(opts: ViteAppOptions): Promise<ViteApp> {
  const original = readFileSync(MESSAGE_FILE, "utf8");

  const proc = spawn(
    process.execPath,
    [join(WEB_DIR, "node_modules", "vite", "bin", "vite.js"), "dev", APP_DIR, "--port", "0"],
    {
      cwd: WEB_DIR,
      stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, PEERPAGES_FIXTURE_BASE: opts.base },
    },
  );

  const watcher = new LineWatcher();
  proc.stdout?.setEncoding("utf8");
  proc.stdout?.on("data", (c: string) => watcher.feed(c));
  proc.stderr?.setEncoding("utf8");
  proc.stderr?.on("data", (c: string) => watcher.feed(c));

  // Wait for the "Local: http://127.0.0.1:PORT/base/" line; because we
  // passed port 0 this is the only place the address comes from.
  const line = await watcher.waitFor(
    (l) => /https?:\/\/127\.0\.0\.1:\d+/.test(stripAnsi(l)),
    60_000,
    "vite dev sunucusunun adresi",
  );
  const match = /https?:\/\/127\.0\.0\.1:\d+[^\s]*/.exec(stripAnsi(line));
  if (!match) {
    throw new Error(`vite adresi okunamadi: ${line}`);
  }
  const targetURL = match[0].replace(/\/+$/, "/");

  const stop = () => {
    writeFileSync(MESSAGE_FILE, original);
    proc.kill();
  };

  return {
    targetURL,
    setMessage: (text: string) => {
      writeFileSync(MESSAGE_FILE, `export const message = ${JSON.stringify(text)};\n`);
    },
    reset: () => writeFileSync(MESSAGE_FILE, original),
    output: () => watcher.all(),
    stop,
    process: proc,
  };
}

/** The Vite output is coloured: strip the ANSI escapes before matching. */
function stripAnsi(s: string): string {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: an ANSI escape
  return s.replace(/\u001b\[[0-9;]*m/g, "");
}
