/**
 * Test helpers that start the agent and the test site as child
 * processes.
 *
 * They automate the manual signaling flow. THE DIRECTION (the phase 2
 * role swap): the offer the page produced is written to the agent's
 * stdin, and the answer the agent printed to stdout is handed to the
 * page. That way the copy-paste path really is tested.
 */

import { type ChildProcessWithoutNullStreams, spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
/** The agent/ directory (web/e2e/fixtures -> web -> repo root -> agent). */
export const AGENT_DIR = resolve(here, "..", "..", "..", "agent");

// The markers the agent prints to stdout (they have to match
// agent/internal/cli/manual.go and agent/internal/cli/testsite.go
// exactly).
const OFFER_PROMPT = "---PEERPAGES-OFFER-WAITING---";
const ANSWER_BEGIN = "---PEERPAGES-ANSWER-BEGIN---";
const ANSWER_END = "---PEERPAGES-ANSWER-END---";
const TUNNEL_OPEN = "---PEERPAGES-TUNNEL-OPEN---";
const SITE_LISTENING = "---PEERPAGES-TESTSITE-LISTENING---";

const BINARY = process.platform === "win32" ? "peerpages.exe" : "peerpages";

/** Builds the agent binary if needed and returns its path. */
export function ensureAgentBinary(): string {
  const binPath = join(AGENT_DIR, BINARY);
  if (existsSync(binPath)) return binPath;

  const build = spawnSync("go", ["build", "-o", BINARY, "."], {
    cwd: AGENT_DIR,
    encoding: "utf8",
  });
  if (build.status !== 0) {
    throw new Error(`ajan derlenemedi: ${build.stderr || build.stdout}`);
  }
  return binPath;
}

/** A helper that watches a child process's stdout line by line, looking for the expected text. */
export class LineWatcher {
  private buffer = "";
  private readonly lines: string[] = [];
  private readonly waiters: Array<{
    match: (line: string) => boolean;
    resolve: (line: string) => void;
    reject: (err: Error) => void;
    timer: ReturnType<typeof setTimeout>;
  }> = [];

  feed(chunk: string): void {
    this.buffer += chunk;
    let idx = this.buffer.indexOf("\n");
    while (idx >= 0) {
      const line = this.buffer.slice(0, idx).replace(/\r$/, "");
      this.buffer = this.buffer.slice(idx + 1);
      this.lines.push(line);
      this.dispatch(line);
      idx = this.buffer.indexOf("\n");
    }
  }

  private dispatch(line: string): void {
    for (let i = this.waiters.length - 1; i >= 0; i--) {
      const w = this.waiters[i];
      if (w?.match(line)) {
        clearTimeout(w.timer);
        this.waiters.splice(i, 1);
        w.resolve(line);
      }
    }
  }

  /** Waits for the first line matching the condition, including lines that already arrived. */
  waitFor(match: (line: string) => boolean, timeoutMs: number, what: string): Promise<string> {
    const already = this.lines.find(match);
    if (already !== undefined) return Promise.resolve(already);

    return new Promise<string>((resolve, reject) => {
      const timer = setTimeout(() => {
        const idx = this.waiters.findIndex((w) => w.timer === timer);
        if (idx >= 0) this.waiters.splice(idx, 1);
        reject(new Error(`${what} ${timeoutMs} ms icinde gelmedi.\nSon ciktilar:\n${this.tail()}`));
      }, timeoutMs);
      this.waiters.push({ match, resolve, reject, timer });
    });
  }

  /** Returns the line after a given one (for the offer base64). */
  lineAfter(marker: string): string | undefined {
    const idx = this.lines.indexOf(marker);
    if (idx < 0) return undefined;
    return this.lines[idx + 1];
  }

  tail(n = 25): string {
    return this.lines.slice(-n).join("\n");
  }

  all(): string[] {
    return [...this.lines];
  }
}

/**
 * Runs the agent binary once and returns its output.
 *
 * For commands that run once and exit, such as `doctor`: the exit code
 * matters too, because some failures (no TURN allocation granted) are
 * reported through the code alone.
 */
export function runAgent(
  args: string[],
  timeoutMs = 90_000,
): Promise<{ code: number | null; stdout: string; stderr: string }> {
  const bin = ensureAgentBinary();
  return new Promise((resolve, reject) => {
    const proc = spawn(bin, args, { cwd: AGENT_DIR, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    proc.stdout.setEncoding("utf8");
    proc.stdout.on("data", (c: string) => {
      stdout += c;
    });
    proc.stderr.setEncoding("utf8");
    proc.stderr.on("data", (c: string) => {
      stderr += c;
    });
    const timer = setTimeout(() => {
      proc.kill();
      reject(new Error(`komut ${timeoutMs} ms icinde bitmedi: ${args.join(" ")}`));
    }, timeoutMs);
    proc.on("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
    proc.on("close", (code) => {
      clearTimeout(timer);
      resolve({ code, stdout, stderr });
    });
  });
}

export interface TestSite {
  url: string;
  stop: () => void;
}

/** Starts the local test site and returns its URL. */
export async function startTestSite(): Promise<TestSite> {
  const bin = ensureAgentBinary();
  const proc = spawn(bin, ["testsite", "--port", "0"], {
    cwd: AGENT_DIR,
    stdio: ["ignore", "pipe", "pipe"],
  });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  const line = await watcher.waitFor(
    (l) => l.startsWith(SITE_LISTENING),
    30_000,
    "test sitesi dinleme satiri",
  );
  const url = line.slice(SITE_LISTENING.length).trim();
  if (!url.startsWith("http://")) {
    throw new Error(`test sitesi URL'si okunamadi: ${line}`);
  }

  return {
    url,
    stop: () => {
      proc.kill();
    },
  };
}

export interface Agent {
  /** Pastes the offer the page produced into the agent and waits for the answer. */
  exchange: (offer: string, timeoutMs?: number) => Promise<string>;
  /** Waits for the marker that reports the tunnel opened. */
  waitTunnelOpen: (timeoutMs?: number) => Promise<void>;
  /** The agent's output (for debugging). */
  output: () => string[];
  stop: () => void;
  process: ChildProcessWithoutNullStreams;
}

/** Starts the agent and waits until it is ready for the offer to be pasted. */
export async function startAgent(targetURL: string, extraArgs: string[] = []): Promise<Agent> {
  const bin = ensureAgentBinary();
  const proc = spawn(bin, ["serve", "--manual", "--target", targetURL, ...extraArgs], {
    cwd: AGENT_DIR,
    stdio: ["pipe", "pipe", "pipe"],
  });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  await watcher.waitFor((l) => l.includes(OFFER_PROMPT), 30_000, "ajanin offer istegi");

  return {
    exchange: async (offer: string, timeoutMs = 60_000) => {
      proc.stdin.write(`${offer.trim()}\n`);
      // ICE gathering (STUN) can take a few seconds.
      await watcher.waitFor((l) => l.includes(ANSWER_END), timeoutMs, "ajan answer'i");
      const answer = watcher.lineAfter(ANSWER_BEGIN);
      if (!answer || answer.length < 20) {
        throw new Error(`answer okunamadi.\nCikti:\n${watcher.tail()}`);
      }
      return answer.trim();
    },
    waitTunnelOpen: async (timeoutMs = 60_000) => {
      await watcher.waitFor((l) => l.includes(TUNNEL_OPEN), timeoutMs, "tunel acik isaretcisi");
    },
    output: () => watcher.all(),
    stop: () => {
      proc.kill();
    },
    process: proc,
  };
}

/** Asks the agent for the expected sha256 of big.bin. */
export function bigFileSHA256(): string {
  const out = spawnSync("go", ["run", "./internal/testsite/gen", "-sha256"], {
    cwd: AGENT_DIR,
    encoding: "utf8",
  });
  if (out.status !== 0) {
    throw new Error(`sha256 alinamadi: ${out.stderr || out.stdout}`);
  }
  return out.stdout.trim();
}
