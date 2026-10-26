/**
 * Ajani ve test sitesini child process olarak baslatan test yardimcilari.
 *
 * Elle signaling akisini otomatiklestirir: ajanin stdout'a bastigi offer
 * okunur, sayfaya verilir, sayfanin urettigi answer ajanin stdin'ine yazilir.
 * Boylece Faz 1'in "kopyala-yapistir" yolu gercekten test edilir (README Faz 1).
 */

import { type ChildProcessWithoutNullStreams, spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
/** agent/ dizini (web/e2e/fixtures -> web -> repo koku -> agent). */
export const AGENT_DIR = resolve(here, "..", "..", "..", "agent");

// Ajanin stdout'a bastigi isaretciler (agent/internal/cli/manual.go,
// agent/internal/cli/testsite.go ile birebir ayni olmali).
const OFFER_BEGIN = "---PEERPAGES-OFFER-BEGIN---";
const OFFER_END = "---PEERPAGES-OFFER-END---";
const TUNNEL_OPEN = "---PEERPAGES-TUNNEL-OPEN---";
const SITE_LISTENING = "---PEERPAGES-TESTSITE-LISTENING---";

const BINARY = process.platform === "win32" ? "peerpages.exe" : "peerpages";

/** Ajan binary'sini gerekirse derler ve yolunu dondurur. */
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

/** Child process'in stdout'unu satir satir izleyip beklenen metni arayan yardimci. */
class LineWatcher {
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

  /** Daha once gelmis satirlar dahil, kosula uyan ilk satiri bekler. */
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

  /** Belirli bir satirdan sonraki satiri dondurur (offer base64'u icin). */
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

export interface TestSite {
  url: string;
  stop: () => void;
}

/** Yerel test sitesini baslatir ve URL'sini dondurur. */
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
  /** Ajanin stdout'a bastigi offer (base64, tek satir). */
  offer: string;
  /** Sayfanin urettigi answer'i ajana yapistirir. */
  sendAnswer: (answer: string) => void;
  /** Tunelin acildigini bildiren isaretciyi bekler. */
  waitTunnelOpen: (timeoutMs?: number) => Promise<void>;
  /** Ajan ciktisi (hata ayiklama icin). */
  output: () => string[];
  stop: () => void;
  process: ChildProcessWithoutNullStreams;
}

/** Ajani baslatir, offer'i okur. */
export async function startAgent(targetURL: string, extraArgs: string[] = []): Promise<Agent> {
  const bin = ensureAgentBinary();
  const proc = spawn(bin, ["serve", "--target", targetURL, ...extraArgs], {
    cwd: AGENT_DIR,
    stdio: ["pipe", "pipe", "pipe"],
  });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  // ICE toplanmasi (STUN) birkac saniye surebilir.
  await watcher.waitFor((l) => l.includes(OFFER_END), 60_000, "ajan offer'i");
  const offer = watcher.lineAfter(OFFER_BEGIN);
  if (!offer || offer.length < 20) {
    throw new Error(`offer okunamadi.\nCikti:\n${watcher.tail()}`);
  }

  return {
    offer: offer.trim(),
    sendAnswer: (answer: string) => {
      proc.stdin.write(`${answer.trim()}\n`);
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

/** big.bin'in beklenen sha256'sini ajandan sorar. */
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
