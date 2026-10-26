/**
 * Nostr yolu icin e2e yardimcilari.
 *
 * Sahte relay kullaniyoruz: gercek internet relay'lerine bagimli bir CI
 * testi kirilgan olur (relay dusebilir, hiz limiti uygulayabilir, ephemeral
 * event'i atabilir). Akisin kendisi burada deterministik dogrulaniyor;
 * gercek relay'ler `peerpages doctor` ile ayrica olculuyor.
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
  stop: () => void;
}

/** Sahte Nostr relay'ini baslatir ve ws:// adresini dondurur. */
export async function startFakeRelay(extraArgs: string[] = []): Promise<FakeRelay> {
  const bin = ensureAgentBinary();
  const proc = spawn(bin, ["fakerelay", "--port", "0", ...extraArgs], {
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

  return {
    url,
    stop: () => {
      proc.kill();
    },
  };
}

export interface NostrAgent {
  /** Ajanin peer-id'si (tam npub). */
  npub: string;
  /** Ziyaretcinin acmasi gereken tam URL (ajanin bastigi). */
  visitorURL: string;
  output: () => string[];
  stop: () => void;
  process: ChildProcess;
}

export interface NostrAgentOptions {
  readonly targetURL: string;
  readonly relayURLs: readonly string[];
  readonly appURL: string;
  readonly secret?: string;
  readonly extraArgs?: string[];
}

/**
 * Ajani NOSTR modunda baslatir (varsayilan mod) ve hazir olmasini bekler.
 *
 * Her cagri icin gecici bir anahtar dizini kullaniyoruz: testler
 * ~/.peerpages/key dosyasina dokunmasin ve her testin peer-id'si farkli olsun.
 */
export async function startNostrAgent(opts: NostrAgentOptions): Promise<NostrAgent> {
  const bin = ensureAgentBinary();
  const keyDir = mkdtempSync(join(tmpdir(), "peerpages-e2e-"));
  const keyPath = join(keyDir, "key");

  const args = ["serve", "--target", opts.targetURL];
  for (const relayURL of opts.relayURLs) {
    args.push("--relay", relayURL);
  }
  args.push("--key", keyPath, "--app-url", opts.appURL);
  if (opts.secret) {
    args.push("--secret", opts.secret);
  }
  args.push(...(opts.extraArgs ?? []));

  const proc = spawn(bin, args, { cwd: AGENT_DIR, stdio: ["ignore", "pipe", "pipe"] });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  await watcher.waitFor((l) => l.includes(READY), 40_000, "ajanin hazir isaretcisi");

  // Ajan ziyaretci URL'sini basiyor; testin elle URL kurmasina gerek yok.
  const urlLine = watcher
    .all()
    .map((l) => l.trim())
    .find((l) => l.startsWith(opts.appURL) && l.includes("#npub1"));
  if (!urlLine) {
    throw new Error(`ziyaretci URL'si bulunamadi.\nCikti:\n${watcher.tail()}`);
  }
  const hashAt = urlLine.indexOf("#");
  const npub = urlLine.slice(hashAt + 1).split(":")[0] ?? "";
  if (!npub.startsWith("npub1")) {
    throw new Error(`peer-id okunamadi: ${urlLine}`);
  }

  return {
    npub,
    visitorURL: urlLine,
    output: () => watcher.all(),
    stop: () => {
      proc.kill();
      try {
        rmSync(keyDir, { recursive: true, force: true });
      } catch {
        // Gecici dizin silinemezse testi dusurmeye deger bir sey degil.
      }
    },
    process: proc,
  };
}
