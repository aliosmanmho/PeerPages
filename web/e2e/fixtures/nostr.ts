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
  /** Dinlenen port; ayni adreste yeniden baslatmak icin (Faz 4). */
  port: number;
  stop: () => void;
}

export interface FakeRelayOptions {
  /**
   * Sabit port. Faz 4'un "relay dusup geri geliyor" testi icin gerekli:
   * 0 ile baslatilan relay yeniden baslatildiginda baska bir adres alir
   * ve ajan eski adresi arar.
   */
  readonly port?: number;
  readonly extraArgs?: readonly string[];
}

/** Sahte Nostr relay'ini baslatir ve ws:// adresini dondurur. */
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
  /** Ajana verilecek TURN adresi (turn:127.0.0.1:port). */
  url: string;
  user: string;
  pass: string;
  stop: () => void;
}

/**
 * Surec ici TURN sunucusunu baslatir (Faz 5).
 *
 * Ajan --ice-relay-only ile yalnizca relay adayi toplamaya zorlanirsa
 * dogrudan yol IMKANSIZ olur; o zaman tunelin acilmasi trafigin TURN'den
 * gectiginin kanitidir.
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
  /**
   * Ajani oldurup AYNI anahtarla yeniden baslatir (Faz 4).
   *
   * Anahtar ayni oldugu icin peer-id de ayni kalir: ziyaretcinin URL'si
   * gecerliligini korur ve sayfanin kendi kendine yeniden baglanmasi
   * olculebilir.
   */
  restart: () => Promise<void>;
  process: ChildProcess;
}

export interface NostrAgentOptions {
  readonly targetURL: string;
  /** Ek hedefler: ad -> yerel adres (Faz 3 coklu hedef). */
  readonly extraTargets?: Readonly<Record<string, string>>;
  readonly relayURLs: readonly string[];
  readonly appURL: string;
  readonly secret?: string;
  /** TURN sunucusu ve kimlik bilgisi (Faz 5). */
  readonly turn?: { url: string; user: string; pass: string };
  /** Yalnizca relay adayi topla: dogrudan yolu imkansiz kilar. */
  readonly iceRelayOnly?: boolean;
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
        // Gecici dizin silinemezse testi dusurmeye deger bir sey degil.
      }
    },
    process: proc,
  };
  return handle;
}
