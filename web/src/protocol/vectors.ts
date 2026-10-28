/**
 * Types and helpers for `protocol/testvectors.json`.
 *
 * This file is used ONLY in tests; it never enters the production bundle.
 * Its Go counterpart is `agent/internal/protocol/vectors_test.go`.
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

export interface WsValidVector {
  readonly name: string;
  readonly binary: boolean;
  readonly fin: boolean;
  readonly payloadHex: string;
  readonly outHex: string;
}

export interface WsDecodeInvalidVector {
  readonly name: string;
  readonly hex: string;
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
  readonly errors: {
    readonly framing: readonly string[];
    readonly ctrl: readonly string[];
    readonly ws: readonly string[];
  };
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
  readonly wsFrames: {
    readonly limits: { readonly headerBytes: number; readonly maxPayload: number };
    readonly valid: readonly WsValidVector[];
    readonly decodeInvalid: readonly WsDecodeInvalidVector[];
  };
}

/** Turns hex text into bytes. Odd-length or invalid input throws. */
export function fromHex(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) {
    throw new Error(`hex length is odd: ${hex.length}`);
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    const byte = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    if (Number.isNaN(byte)) {
      throw new Error(`invalid hex: ${hex.slice(i * 2, i * 2 + 2)}`);
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
 * Turns a vector's generation recipe into bytes. Same rules as the Go
 * side:
 *  - `repeat`: the `byte` value `len` times,
 *  - `counter`: byte i is `i % 256`.
 */
export function buildPattern(p: Pattern): Uint8Array {
  const out = new Uint8Array(p.len);
  if (p.kind === "repeat") {
    if (p.byte === undefined) {
      throw new Error("the repeat pattern requires byte");
    }
    out.fill(p.byte);
    return out;
  }
  for (let i = 0; i < p.len; i++) {
    out[i] = i % 256;
  }
  return out;
}
