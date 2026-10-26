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
  /** Ziyaretcinin acmasi gereken tam URL (ajanin bastigi, varsayilan hedef). */
  visitorURL: string;
  /**
   * Hedef adi -> ajanin bastigi ziyaretci URL'si.
   *
   * Coklu hedefte (Faz 3) ajan her hedef icin ayri bir URL basiyor; test
   * kendi eliyle URL kurmasin.
   */
  urls: Record<string, string>;
  output: () => string[];
  stop: () => void;
  process: ChildProcess;
}

export interface NostrAgentOptions {
  readonly targetURL: string;
  /** Ek hedefler: ad -> yerel adres (Faz 3 coklu hedef). */
  readonly extraTargets?: Readonly<Record<string, string>>;
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
  args.push(...(opts.extraArgs ?? []));

  const proc = spawn(bin, args, { cwd: AGENT_DIR, stdio: ["ignore", "pipe", "pipe"] });

  const watcher = new LineWatcher();
  proc.stdout.setEncoding("utf8");
  proc.stdout.on("data", (c: string) => watcher.feed(c));
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (c: string) => watcher.feed(c));

  await watcher.waitFor((l) => l.includes(READY), 40_000, "ajanin hazir isaretcisi");

  // Ajan her hedef icin ziyaretci URL'sini basiyor; testin elle URL
  // kurmasina gerek yok.
  const urlLines = watcher
    .all()
    .map((l) => l.trim())
    .filter((l) => l.startsWith(opts.appURL) && l.includes("#npub1"));
  if (urlLines.length === 0) {
    throw new Error(`ziyaretci URL'si bulunamadi: ${watcher.tail()}`);
  }

  // Fragment dilbilgisi: npub [ "/" hedef ] [ ":" sir ]
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

  return {
    npub,
    visitorURL: urlLine,
    urls,
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
