/**
 * The e2e helpers for the Nostr path.
 *
 * We use a fake relay: a CI test that depends on real internet relays is
 * fragile (a relay can go down, rate-limit, or drop an ephemeral event).
 * The flow itself is verified deterministically here; the real relays are
 * measured separately with `peerpages doctor`.
 */

import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { AGENT_DIR, ensureAgentBinary, LineWatcher } from "./agent";

const FAKERELAY_LISTENING = "---PEERPAGES-FAKERELAY-LISTENING---";
const READY = "---PEERPAGES-READY---";

export interface FakeRelay {
  url: string;
  /** The port being listened on, so it can restart on the same address (phase 4). */
  port: number;
  stop: () => void;
}

export interface FakeRelayOptions {
  /**
   * A fixed port. Needed for the phase 4 "a relay goes down and comes
   * back" test: a relay started with 0 gets a different address when it
   * restarts, and the agent keeps looking for the old one.
   */
  readonly port?: number;
  readonly extraArgs?: readonly string[];
}

/** Starts the fake Nostr relay and returns its ws:// address. */
export async function startFakeRelay(
  opts: readonly string[] | FakeRelayOptions = {},
): Promise<FakeRelay> {
  const o: FakeRelayOptions = Array.isArray(opts)
    ? { extraArgs: opts }
    : (opts as FakeRelayOptions);
  const bin = ensureAgentBinary();
  const proc = spawn(bin, ["fakerelay", "--port", String(o.port ?? 0), ...(o.extraArgs ?? [])], {
    cwd: AGENT_DIR,
    stdio: ["ignore", "pipe", "pipe"],
  });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  const line = await watcher.waitFor(
    (l) => l.startsWith(FAKERELAY_LISTENING),
    30_000,
    "sahte relay dinleme satiri",
  );
  const url = line.slice(FAKERELAY_LISTENING.length).trim();
  if (!url.startsWith("ws://")) {
    throw new Error(`sahte relay adresi okunamadi: ${line}`);
  }

  const port = Number(new URL(url).port);
  if (!Number.isFinite(port) || port === 0) {
    throw new Error(`sahte relay portu okunamadi: ${url}`);
  }

  return {
    url,
    port,
    stop: () => {
      proc.kill();
    },
  };
}

const FAKETURN_LISTENING = "---PEERPAGES-FAKETURN-LISTENING---";

export interface FakeTurn {
  /** The TURN address to hand to the agent (turn:127.0.0.1:port). */
  url: string;
  user: string;
  pass: string;
  stop: () => void;
}

/**
 * Starts the in-process TURN server (phase 5).
 *
 * Once the agent is forced with --ice-relay-only to gather relay
 * candidates only, the direct path is IMPOSSIBLE; then the tunnel opening
 * is proof that the traffic went over TURN.
 */
export async function startFakeTurn(): Promise<FakeTurn> {
  const bin = ensureAgentBinary();
  const user = "peerpages";
  const pass = "test-password";
  const proc = spawn(bin, ["faketurn", "--port", "0", "--user", user, "--pass", pass], {
    cwd: AGENT_DIR,
    stdio: ["ignore", "pipe", "pipe"],
  });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  const line = await watcher.waitFor(
    (l) => l.startsWith(FAKETURN_LISTENING),
    30_000,
    "sahte TURN dinleme satiri",
  );
  const url = line.slice(FAKETURN_LISTENING.length).trim();
  if (!url.startsWith("turn:")) {
    throw new Error(`sahte TURN adresi okunamadi: ${line}`);
  }

  return {
    url,
    user,
    pass,
    stop: () => {
      proc.kill();
    },
  };
}

export interface NostrAgent {
  /** The agent's peer-id (the full npub). */
  npub: string;
  /** The full URL the visitor should open (the one the agent printed, for the default target). */
  visitorURL: string;
  /**
   * Target name -> the visitor URL the agent printed.
   *
   * With several targets (phase 3) the agent prints a separate URL for
   * each one, so the test should not build URLs by hand.
   */
  urls: Record<string, string>;
  output: () => string[];
  stop: () => void;
  /**
   * Kills the agent and restarts it with the SAME key (phase 4).
   *
   * Because the key is the same the peer-id stays the same: the visitor's
   * URL keeps working and the page reconnecting on its own can be
   * measured.
   */
  restart: () => Promise<void>;
  process: ChildProcess;
}

export interface NostrAgentOptions {
  readonly targetURL: string;
  /** Extra targets: name -> local address (the phase 3 several-targets case). */
  readonly extraTargets?: Readonly<Record<string, string>>;
  readonly relayURLs: readonly string[];
  readonly appURL: string;
  readonly secret?: string;
  /** The TURN server and its credentials (phase 5). */
  readonly turn?: { url: string; user: string; pass: string };
  /** Gather relay candidates only: it makes the direct path impossible. */
  readonly iceRelayOnly?: boolean;
  readonly extraArgs?: string[];
}

/**
 * Starts the agent in NOSTR mode (the default) and waits until it is
 * ready.
 *
 * Every call uses a temporary key directory: the tests must not touch
 * ~/.peerpages/key, and every test gets its own peer-id.
 */
export async function startNostrAgent(opts: NostrAgentOptions): Promise<NostrAgent> {
  const bin = ensureAgentBinary();
  const keyDir = mkdtempSync(join(tmpdir(), "peerpages-e2e-"));
  const keyPath = join(keyDir, "key");

  const args = ["serve", "--target", opts.targetURL];
  for (const [name, url] of Object.entries(opts.extraTargets ?? {})) {
    args.push("--target", `${name}=${url}`);
  }
  for (const relayURL of opts.relayURLs) {
    args.push("--relay", relayURL);
  }
  args.push("--key", keyPath, "--app-url", opts.appURL);
  if (opts.secret) {
    args.push("--secret", opts.secret);
  }
  if (opts.turn) {
    args.push(
      "--turn",
      opts.turn.url,
      "--turn-user",
      opts.turn.user,
      "--turn-pass",
      opts.turn.pass,
    );
  }
  if (opts.iceRelayOnly) {
    args.push("--ice-relay-only");
  }
  args.push(...(opts.extraArgs ?? []));

  let watcher = new LineWatcher();
  const attach = (p: ChildProcess) => {
    p.stdout?.setEncoding("utf8");
    p.stdout?.on("data", (c: string) => watcher.feed(c));
    p.stderr?.setEncoding("utf8");
    p.stderr?.on("data", (c: string) => watcher.feed(c));
  };

  let proc = spawn(bin, args, { cwd: AGENT_DIR, stdio: ["ignore", "pipe", "pipe"] });
  attach(proc);

  await watcher.waitFor((l) => l.includes(READY), 40_000, "ajanin hazir isaretcisi");

  // The agent prints the visitor URL for every target, so the test does
  // not have to build one by hand.
  const urlLines = watcher
    .all()
    .map((l) => l.trim())
    .filter((l) => l.startsWith(opts.appURL) && l.includes("#npub1"));
  if (urlLines.length === 0) {
    throw new Error(`the visitor URL was not found: ${watcher.tail()}`);
  }

  // The fragment grammar: npub [ "/" target ] [ ":" secret ]
  const urls: Record<string, string> = {};
  let npub = "";
  for (const line of urlLines) {
    const frag = line.slice(line.indexOf("#") + 1);
    const head = frag.split(":")[0] ?? "";
    const slash = head.indexOf("/");
    const id = slash >= 0 ? head.slice(0, slash) : head;
    const target = slash >= 0 ? head.slice(slash + 1) : "default";
    if (!id.startsWith("npub1")) {
      throw new Error(`peer-id okunamadi: ${line}`);
    }
    npub = id;
    urls[target] = line;
  }
  const urlLine = urls.default ?? urlLines[0] ?? "";

  const handle: NostrAgent = {
    npub,
    visitorURL: urlLine,
    urls,
    output: () => watcher.all(),
    restart: async () => {
      const dead = new Promise<void>((resolve) => proc.once("exit", () => resolve()));
      proc.kill();
      await dead;

      watcher = new LineWatcher();
      proc = spawn(bin, args, { cwd: AGENT_DIR, stdio: ["ignore", "pipe", "pipe"] });
      attach(proc);
      await watcher.waitFor((l) => l.includes(READY), 40_000, "yeniden baslayan ajan");
      handle.process = proc;
    },
    stop: () => {
      proc.kill();
      try {
        rmSync(keyDir, { recursive: true, force: true });
      } catch {
        // A temporary directory that cannot be removed is not worth
        // failing the test over.
      }
    },
    process: proc,
  };
  return handle;
}
