import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { encode, MAX_VERSION, penalty, QRError } from "./qr";

// Vektorler ucuncu bir bagimsiz uygulama (python qrcode) ile uretildi:
// scripts/gen-qr-vectors.py
const vectorsPath = fileURLToPath(new URL("./qr-testvectors.json", import.meta.url));

interface Vectors {
  errorCorrection: string;
  mode: string;
  maxVersion: number;
  forced: Array<{
    name: string;
    text: string;
    mask: number;
    version: number;
    size: number;
    modules: string[];
  }>;
  auto: Array<{ name: string; text: string; version: number; size: number; modules: string[] }>;
  limits: { maxBytes: number };
}

const V: Vectors = JSON.parse(readFileSync(vectorsPath, "utf8"));

function rows(modules: readonly boolean[][]): string[] {
  return modules.map((row) => row.map((cell) => (cell ? "1" : "0")).join(""));
}

/** Tek modul okur (noUncheckedIndexedAccess altinda kisa yol). */
function at(code: { modules: readonly boolean[][] }, r: number, c: number): boolean {
  return code.modules[r]?.[c] === true;
}

describe("qr-testvectors.json", () => {
  it("kodlayicinin kapsamiyla ayni", () => {
    expect(V.errorCorrection).toBe("M");
    expect(V.mode).toBe("byte");
    expect(V.maxVersion).toBe(MAX_VERSION);
  });
});

// Maske zorlanmis vektorler boru hattinin tamamini kapsar: bit akisi,
// Reed-Solomon, serpistirme, zikzak yerlestirme, bicim ve surum bilgisi.
describe("encode — zorlanmis maske, bayt bayt", () => {
  for (const vec of V.forced) {
    it(`${vec.name} (maske ${vec.mask}, surum ${vec.version})`, () => {
      const got = encode(vec.text, { mask: vec.mask });
      expect(got.version).toBe(vec.version);
      expect(got.size).toBe(vec.size);
      expect(got.mask).toBe(vec.mask);
      expect(rows(got.modules)).toEqual(vec.modules);
    });
  }
});

// Otomatik secim penalti kurallarini kullanir; kutuphane de ayni kurallari
// uyguladigi icin sonuc matrisi birebir aynı olmali.
describe("encode — otomatik maske secimi", () => {
  for (const vec of V.auto) {
    it(vec.name, () => {
      const got = encode(vec.text);
      expect(got.version).toBe(vec.version);
      expect(rows(got.modules)).toEqual(vec.modules);
    });
  }

  it("secilen maske en dusuk penaltiyi veriyor", () => {
    const text = "https://kullanici.github.io/PeerPages/#npub1deneme";
    const auto = encode(text);
    const autoScore = penalty(auto.modules);
    for (let mask = 0; mask < 8; mask++) {
      expect(autoScore).toBeLessThanOrEqual(penalty(encode(text, { mask }).modules));
    }
  });
});

// Vektorler dogru olsa bile yapisal degismezleri ayrica kontrol etmek
// faydali: bir hata tum vektorleri birlikte kaydirirsa buradan yakalanir.
describe("yapisal degismezler", () => {
  const code = encode("PeerPages yapisal kontrol");

  it("boyut 4 * surum + 17", () => {
    expect(code.size).toBe(4 * code.version + 17);
    expect(code.modules.length).toBe(code.size);
    for (const row of code.modules) expect(row.length).toBe(code.size);
  });

  it("uc bulucu desen yerinde", () => {
    const corners: Array<[number, number]> = [
      [0, 0],
      [0, code.size - 7],
      [code.size - 7, 0],
    ];
    for (const [r0, c0] of corners) {
      for (let r = 0; r < 7; r++) {
        for (let c = 0; c < 7; c++) {
          const ring = Math.max(Math.abs(r - 3), Math.abs(c - 3));
          // Halka 3 ve 0-1 koyu, halka 2 acik.
          expect(at(code, r0 + r, c0 + c)).toBe(ring !== 2);
        }
      }
    }
  });

  it("zamanlama desenleri donusumlu", () => {
    for (let i = 8; i < code.size - 8; i++) {
      expect(at(code, 6, i)).toBe(i % 2 === 0);
      expect(at(code, i, 6)).toBe(i % 2 === 0);
    }
  });

  it("koyu modul koyu", () => {
    expect(at(code, code.size - 8, 8)).toBe(true);
  });

  it("dorduncu kose bos (bulucu desen yok)", () => {
    // Sag alt kosede bulucu desen OLMAMALI: QR'in yonunu bu belirler.
    const r = code.size - 7;
    const c = code.size - 7;
    let allDark = true;
    for (let i = 0; i < 7; i++) {
      if (!at(code, r, c + i)) allDark = false;
    }
    expect(allDark).toBe(false);
  });
});

describe("encode — girdi kontrolleri", () => {
  it("bos metni reddeder", () => {
    expect(() => encode("")).toThrow(QRError);
  });

  it("gecersiz maskeyi reddeder", () => {
    expect(() => encode("x", { mask: 8 })).toThrow(QRError);
    expect(() => encode("x", { mask: -1 })).toThrow(QRError);
    expect(() => encode("x", { mask: 1.5 })).toThrow(QRError);
  });

  it("kapasite sinirinda calisir, bir fazlasini reddeder", () => {
    const max = V.limits.maxBytes;
    expect(encode("a".repeat(max)).version).toBe(MAX_VERSION);
    expect(() => encode("a".repeat(max + 1))).toThrow(/too long/);
  });

  // Byte modu BAYT sayar: cok baytli karakterler kapasiteyi daha hizli yer.
  it("UTF-8 baytlari dogru sayilir", () => {
    const ascii = encode("a".repeat(60));
    const turkce = encode("ç".repeat(60)); // her biri 2 bayt
    expect(turkce.version).toBeGreaterThan(ascii.version);
  });

  it("surum metin uzadikca buyur", () => {
    let last = 0;
    for (const len of [5, 30, 60, 100, 150, 200]) {
      const v = encode("x".repeat(len)).version;
      expect(v).toBeGreaterThanOrEqual(last);
      last = v;
    }
  });
});
