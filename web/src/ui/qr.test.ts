import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { encode, MAX_VERSION, penalty, QRError } from "./qr";

// The vectors were produced with a third, independent implementation
// (python qrcode): scripts/gen-qr-vectors.py
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

/** Reads one module (a shortcut under noUncheckedIndexedAccess). */
function at(code: { modules: readonly boolean[][] }, r: number, c: number): boolean {
  return code.modules[r]?.[c] === true;
}

describe("qr-testvectors.json", () => {
  it("it matches the encoder's scope", () => {
    expect(V.errorCorrection).toBe("M");
    expect(V.mode).toBe("byte");
    expect(V.maxVersion).toBe(MAX_VERSION);
  });
});

// The forced-mask vectors cover the whole pipeline: the bit stream,
// Reed-Solomon, interleaving, the zigzag placement, and the format and
// version information.
describe("encode — a forced mask, byte for byte", () => {
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

// The automatic choice uses the penalty rules; because the library
// applies the same rules the resulting matrix has to be identical.
describe("encode — the automatic mask choice", () => {
  for (const vec of V.auto) {
    it(vec.name, () => {
      const got = encode(vec.text);
      expect(got.version).toBe(vec.version);
      expect(rows(got.modules)).toEqual(vec.modules);
    });
  }

  it("the chosen mask gives the lowest penalty", () => {
    const text = "https://kullanici.github.io/PeerPages/#npub1deneme";
    const auto = encode(text);
    const autoScore = penalty(auto.modules);
    for (let mask = 0; mask < 8; mask++) {
      expect(autoScore).toBeLessThanOrEqual(penalty(encode(text, { mask }).modules));
    }
  });
});

// Even with the vectors correct it is worth checking the structural
// invariants separately: if a bug shifts every vector together, this is
// where it gets caught.
describe("the structural invariants", () => {
  const code = encode("PeerPages yapisal kontrol");

  it("the size is 4 * version + 17", () => {
    expect(code.size).toBe(4 * code.version + 17);
    expect(code.modules.length).toBe(code.size);
    for (const row of code.modules) expect(row.length).toBe(code.size);
  });

  it("the three finder patterns are in place", () => {
    const corners: Array<[number, number]> = [
      [0, 0],
      [0, code.size - 7],
      [code.size - 7, 0],
    ];
    for (const [r0, c0] of corners) {
      for (let r = 0; r < 7; r++) {
        for (let c = 0; c < 7; c++) {
          const ring = Math.max(Math.abs(r - 3), Math.abs(c - 3));
          // Rings 3 and 0-1 are dark, ring 2 is light.
          expect(at(code, r0 + r, c0 + c)).toBe(ring !== 2);
        }
      }
    }
  });

  it("the timing patterns alternate", () => {
    for (let i = 8; i < code.size - 8; i++) {
      expect(at(code, 6, i)).toBe(i % 2 === 0);
      expect(at(code, i, 6)).toBe(i % 2 === 0);
    }
  });

  it("a dark module is dark", () => {
    expect(at(code, code.size - 8, 8)).toBe(true);
  });

  it("the fourth corner is empty (no finder pattern)", () => {
    // There must be NO finder pattern in the bottom-right corner: that
    // is what sets the QR's orientation.
    const r = code.size - 7;
    const c = code.size - 7;
    let allDark = true;
    for (let i = 0; i < 7; i++) {
      if (!at(code, r, c + i)) allDark = false;
    }
    expect(allDark).toBe(false);
  });
});

describe("encode — the input checks", () => {
  it("it refuses empty text", () => {
    expect(() => encode("")).toThrow(QRError);
  });

  it("it refuses an invalid mask", () => {
    expect(() => encode("x", { mask: 8 })).toThrow(QRError);
    expect(() => encode("x", { mask: -1 })).toThrow(QRError);
    expect(() => encode("x", { mask: 1.5 })).toThrow(QRError);
  });

  it("it works at the capacity limit and refuses one more", () => {
    const max = V.limits.maxBytes;
    expect(encode("a".repeat(max)).version).toBe(MAX_VERSION);
    expect(() => encode("a".repeat(max + 1))).toThrow(/too long/);
  });

  // Byte mode counts BYTES: multi-byte characters eat the capacity
  // faster.
  it("the UTF-8 bytes are counted correctly", () => {
    const ascii = encode("a".repeat(60));
    const turkce = encode("ç".repeat(60)); // her biri 2 bayt
    expect(turkce.version).toBeGreaterThan(ascii.version);
  });

  it("the version grows as the text gets longer", () => {
    let last = 0;
    for (const len of [5, 30, 60, 100, 150, 200]) {
      const v = encode("x".repeat(len)).version;
      expect(v).toBeGreaterThanOrEqual(last);
      last = v;
    }
  });
});
