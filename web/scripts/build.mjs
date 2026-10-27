#!/usr/bin/env node
/**
 * Web derlemesi: app shell + elle derlenen Service Worker.
 *
 * Neden bir script: `base` degerini ortam degiskeniyle vermek gerekiyor ve
 * bunu npm scriptinde platformdan bagimsiz yapmanin temiz bir yolu yok
 * (Windows'ta `VAR=x cmd` calismaz, Git Bash ise `/PeerPages/` degerini
 * Windows yoluna cevirir). Node araya girince iki sorun da bitiyor.
 *
 * Kullanim:
 *   node scripts/build.mjs             # base "/"
 *   node scripts/build.mjs /PeerPages/ # GitHub Pages alt yolu
 */

import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const base = process.argv[2] ?? process.env.PEERPAGES_BASE ?? "/";

if (/^[A-Za-z]:/.test(base)) {
  // Git Bash (MSYS) "/PeerPages/" argumanini Windows yoluna cevirmis.
  console.error(
    `base bir Windows yoluna cevrilmis: ${base}
` +
      "Git Bash kullaniyorsan: MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/",
  );
  process.exit(1);
}
if (!base.startsWith("/") || !base.endsWith("/")) {
  console.error(`base "/" ile baslayip "/" ile bitmeli, verilen: ${base}`);
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
    console.error(`basarisiz: npx ${args.join(" ")}`);
    process.exit(res.status ?? 1);
  }
}

// SIRA ONEMLI: sonraki derlemeler emptyOutDir: false ile calisir, app
// shell'i silmemeleri icin ondan sonra gelmeli.
run(["vite", "build"]);
run(["vite", "build", "--config", "vite.sw.config.ts"]);
run(["vite", "build", "--config", "vite.shim.config.ts"]);

console.log(`web derlendi (base=${base})`);
