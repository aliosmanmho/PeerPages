#!/usr/bin/env node
// Guards against Turkish left over in the repository (phase 8).
//
// Why a word list and not just a diacritic grep: the comments were written in
// ASCII-ised Turkish ("govde", "cerceve", "gecersiz"), so a `[cgiosu]` grep
// finds about 1% of the real thing. The two checks together are what works:
//
//   1. a list of ASCII-ised Turkish words, matched on word boundaries,
//   2. a regex for the Turkish-specific letters.
//
// Exit code 1 on any hit, so it can go into CI once the Actions billing lock
// is lifted.

import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";

const ROOT = path.resolve(import.meta.dirname, "..");

// Directories and files that are never scanned.
const SKIP_DIRS = ["node_modules", "dist", ".git", "docs/img", "test-results"];
const SKIP_FILES = [
  "scripts/check-language.mjs",
  "package-lock.json",
  // Turkish on purpose: the dictionary itself and the tests that assert it.
  "web/src/i18n.ts",
  "web/src/i18n.test.ts",
  "web/e2e/i18n.spec.ts",
];

// The language's own name is not a leftover.
const ALLOW = [/Türkçe/g];

// Binary and generated extensions.
const SKIP_EXT = new Set([
  ".png", ".jpg", ".jpeg", ".gif", ".ico", ".webp", ".pdf", ".zip", ".gz",
  ".bin", ".woff", ".woff2", ".ttf", ".sum", ".lock",
]);

// Deliberately kept Turkish: the historical prompt in BRIEF section 10. The
// file is scanned up to that heading and no further.
const STOP_AT = new Map([["docs/BRIEF.md", /^## 10\./]]);

const WORDS = [
  "ajan", "akis", "alan", "anahtar", "aninda", "ardindan", "arayuz", "ayni",
  "ayrica", "ayristirma", "baglanti", "baglanma", "baglaniyor", "bagimlilik",
  "baslangic", "baslik", "basarili", "basarisiz", "bayrak", "bekleniyor",
  "bicim", "bilgi", "bolum", "boyle", "bundan", "buyuk", "calisiyor",
  "calisma", "cerceve", "cevap", "cikis", "cikti", "cozum", "coklu", "cunku",
  "dahil", "degeri", "degil", "degisiklik", "degismedi", "denetim", "deneme",
  "dizin", "dogrulama", "dogru", "dosya", "durum", "disinda", "eksik",
  "elle", "eszamanli", "gecerli", "gecersiz", "gecikme", "gelen", "gerek",
  "gereksiz", "gercek", "gonderilen", "gorsel", "gorunur", "govde", "guvenlik",
  "hatasi", "hedef", "henuz", "hicbir", "icerik", "icin", "ilke", "istek",
  "izin", "kabul", "kanal", "kapali", "kapsam", "karar", "kaynak", "kimlik",
  "korunuyor", "kullanici", "kullanim", "listesi", "maskeleme", "mesaj",
  "mevcut", "neden", "nedeni", "olarak", "olculen", "olcum",
  "onceki", "onerilen", "oturum", "ornek", "ozellik", "parca", "sadece",
  "sayfa", "sayac", "sebep", "secenek", "secim", "sifre", "sinir", "sira",
  "siralama", "sonra", "sunucu", "surum", "tanim", "tarafinda",
  "tasiyor", "tavan", "testler", "tunel", "ustunde", "uzerinde", "varsayilan",
  "veri", "yalnizca", "yani", "yanit", "yapilandirma", "yeni", "yerine",
  "yok", "yol", "yukari", "zaman", "zorunlu",
];

const WORD_RE = new RegExp(`\\b(${WORDS.join("|")})\\b`, "gi");
const DIACRITIC_RE = /[çÇğĞıİşŞöÖüÜ]/g;

function tracked() {
  const out = execFileSync("git", ["ls-files", "-z"], { cwd: ROOT, encoding: "utf8" });
  return out.split("\0").filter(Boolean);
}

const hits = [];

for (const rel of tracked()) {
  if (SKIP_FILES.includes(rel)) continue;
  if (SKIP_DIRS.some((d) => rel === d || rel.startsWith(`${d}/`) || rel.includes(`/${d}/`))) continue;
  if (SKIP_EXT.has(path.extname(rel).toLowerCase())) continue;

  let text;
  try {
    text = readFileSync(path.join(ROOT, rel), "utf8");
  } catch {
    continue;
  }
  if (text.includes("\0")) continue;

  const stop = STOP_AT.get(rel);
  const markdown = path.extname(rel).toLowerCase() === ".md";
  const lines = text.split(/\r?\n/);

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (stop && stop.test(line)) break;
    // An explicit escape for a line that is Turkish on purpose: a test
    // vector, or a quotation of an identifier that used to be Turkish.
    if (line.includes("lang-ok")) continue;

    // In the documents a backtick span is a quotation of a name, not prose;
    // blank those out before matching.
    let subject = markdown ? line.replace(/`[^`]*`/g, "``") : line;
    for (const re of ALLOW) subject = subject.replace(re, "");

    const words = [...subject.matchAll(WORD_RE)].map((m) => m[0]);
    const marks = [...subject.matchAll(DIACRITIC_RE)].map((m) => m[0]);
    const found = [...new Set([...words, ...marks])];
    if (found.length > 0) {
      hits.push({ rel, line: i + 1, found, text: line.trim() });
    }
  }
}

for (const h of hits) {
  console.log(`${h.rel}:${h.line}: [${h.found.join(" ")}] ${h.text.slice(0, 120)}`);
}

if (hits.length > 0) {
  console.error(`\n${hits.length} line(s) with leftover Turkish.`);
  process.exit(1);
}

console.log("No leftover Turkish found.");
