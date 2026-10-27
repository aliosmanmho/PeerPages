/**
 * Vite dev sunucusu fixture'i (Faz 6).
 *
 * Kabul kriterinin asil yarisi: "Vite HMR tunel uzerinden calisiyor". Sahte
 * bir ws ucu bunu kanitlamaz — HMR'in kendi el sikismasi (alt protokol
 * `vite-hmr`, token, modul guncelleme mesajlari) gercek sunucuyla
 * olculmeli. `vite` zaten devDependency, yeni bagimlilik gerekmiyor.
 */

import { type ChildProcess, spawn } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { LineWatcher } from "./agent";

const WEB_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const APP_DIR = join(WEB_DIR, "e2e", "fixtures", "vite-app");
const MESSAGE_FILE = join(APP_DIR, "src", "mesaj.js");

export interface ViteApp {
  /** Ajana verilecek hedef (base oneki DAHIL). */
  targetURL: string;
  /** `src/mesaj.js`'i degistirir; HMR bunu yakalamali. */
  setMessage: (text: string) => void;
  /** Dosyayi ilk haline dondurur. */
  reset: () => void;
  output: () => string[];
  stop: () => void;
  process: ChildProcess;
}

export interface ViteAppOptions {
  /** App shell'in scope koku; fixture'in `base` degeri bununla AYNI olmali. */
  readonly base: string;
}

/**
 * Fixture projesini `vite dev` ile baslatir.
 *
 * `base` neden onemli: Service Worker scope onekini kirparak ajana yolluyor,
 * Vite ise onekli yollar bekliyor. Ikisini hizalamak icin fixture'in base'i
 * scope koku, ajanin hedefi de ayni oneki tasiyan URL olur. Bu ayni zamanda
 * Vite'in urettigi KOK-MUTLAK yollarin scope icinde kalmasini sagliyor
 * (BRIEF §8'deki yapisal sinir).
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

  // "Local: http://127.0.0.1:PORT/base/" satirini bekle; port 0 verdigimiz
  // icin adresi yalnizca buradan ogrenebiliyoruz.
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
      writeFileSync(MESSAGE_FILE, `export const mesaj = ${JSON.stringify(text)};\n`);
    },
    reset: () => writeFileSync(MESSAGE_FILE, original),
    output: () => watcher.all(),
    stop,
    process: proc,
  };
}

/** Vite ciktisi renkli: eslesmeler icin ANSI kacislarini temizle. */
function stripAnsi(s: string): string {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI kacisi
  return s.replace(/\u001b\[[0-9;]*m/g, "");
}
