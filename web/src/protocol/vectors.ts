/**
 * `protocol/testvectors.json` icin tip ve yardimcilar.
 *
 * Bu dosya YALNIZCA testlerde kullanilir; uretim bundle'ina girmez.
 * Go tarafindaki karsiligi `agent/internal/protocol/vectors_test.go`.
 */

export interface Pattern {
  readonly kind: "repeat" | "counter";
  readonly byte?: number;
  readonly len: number;
}

export interface ValidFrameVector {
  readonly name: string;
  readonly id: number;
  readonly seq: number;
  readonly payloadHex: string;
  readonly frameHex: string;
}

export interface GeneratedFrameVector {
  readonly name: string;
  readonly id: number;
  readonly seq: number;
  readonly pattern: Pattern;
}

export interface EncodeInvalidVector {
  readonly name: string;
  readonly id: number;
  readonly seq: number;
  readonly pattern: Pattern;
  readonly error: string;
}

export interface DecodeInvalidVector {
  readonly name: string;
  readonly frame: {
    readonly hex?: string;
    readonly id?: number;
    readonly seq?: number;
    readonly pattern?: Pattern;
  };
  readonly error: string;
}

export interface CtrlValidVector {
  readonly name: string;
  readonly text: string;
  readonly canonical: string;
}

export interface CtrlInvalidVector {
  readonly name: string;
  readonly text: string;
  readonly error: string;
}

export interface TestVectors {
  readonly version: number;
  readonly note: string;
  readonly framing: {
    readonly headerBytes: number;
    readonly maxPayload: number;
    readonly maxFrame: number;
  };
  readonly errors: { readonly framing: readonly string[]; readonly ctrl: readonly string[] };
  readonly frames: {
    readonly valid: readonly ValidFrameVector[];
    readonly generated: readonly GeneratedFrameVector[];
    readonly encodeInvalid: readonly EncodeInvalidVector[];
    readonly decodeInvalid: readonly DecodeInvalidVector[];
  };
  readonly ctrl: {
    readonly valid: readonly CtrlValidVector[];
    readonly invalid: readonly CtrlInvalidVector[];
  };
}

/** Hex metni bayta cevirir. Tek haneli ya da gecersiz girdi hata verir. */
export function fromHex(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) {
    throw new Error(`hex uzunlugu tek: ${hex.length}`);
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    const byte = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    if (Number.isNaN(byte)) {
      throw new Error(`gecersiz hex: ${hex.slice(i * 2, i * 2 + 2)}`);
    }
    out[i] = byte;
  }
  return out;
}

export function toHex(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) {
    s += b.toString(16).padStart(2, "0");
  }
  return s;
}

/**
 * Vektordeki uretim tarifini bayta cevirir. Go tarafiyla ayni kurallar:
 *  - `repeat`: `byte` degeri `len` kez,
 *  - `counter`: i. bayt `i % 256`.
 */
export function buildPattern(p: Pattern): Uint8Array {
  const out = new Uint8Array(p.len);
  if (p.kind === "repeat") {
    if (p.byte === undefined) {
      throw new Error("repeat deseni icin byte gerekli");
    }
    out.fill(p.byte);
    return out;
  }
  for (let i = 0; i < p.len; i++) {
    out[i] = i % 256;
  }
  return out;
}
