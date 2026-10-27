/**
 * WebSocket koprusunun cerceve alt bicimi (Faz 6).
 *
 * Dis cerceve (`[4B id][4B seq][payload]`, `framing.ts`) AYNEN korunur: ws
 * yuku ayri bir kanal ya da ayri bir framing kullanmaz. Bir ws id'sine ait
 * cercevenin PAYLOAD'i su bicimdedir:
 *
 *     [1B bayrak][parca]
 *
 * Boylece backpressure, seq denetimi ve 16 KiB parcalama bedavaya gelir;
 * ikili veri base64 sismesi yemez ve toplu ws trafigi `ctrl` kanalini (ping,
 * abort) tikamaz.
 *
 * Normatif tanim: `protocol/README.md`. Go karsiligi
 * `agent/internal/protocol/wsframe.go`; iki taraf ayni vektorleri okur.
 */

import { MAX_PAYLOAD } from "./framing";

/** Bayrak: 1 ise ikili, 0 ise metin mesaji. */
export const WS_FLAG_BINARY = 0x01;

/** Bayrak: 1 ise bu, ws mesajinin SON parcasi. */
export const WS_FLAG_FIN = 0x02;

/** Tanimli bitler; disindaki bitler protokol hatasi. */
export const WS_FLAG_KNOWN = WS_FLAG_BINARY | WS_FLAG_FIN;

/** ws alt basligi: yalnizca bayrak bayti. */
export const WS_HEADER_BYTES = 1;

/** Bir ws parcasinin en buyuk boyu. */
export const MAX_WS_PAYLOAD = MAX_PAYLOAD - WS_HEADER_BYTES;

/** ws cerceve hata kodlari — iki dilde ayni. */
export type WsFrameErrorCode = "ws-empty-frame" | "ws-bad-flags" | "ws-payload-too-large";

/** ws cerceve hatasi. `code` iki dilde aynidir; testvectors.json buna gore dogrular. */
export class WsFrameError extends Error {
  readonly code: WsFrameErrorCode;

  constructor(code: WsFrameErrorCode, detail: string) {
    super(`${code}: ${detail}`);
    this.name = "WsFrameError";
    this.code = code;
  }
}

export interface WsChunk {
  /** Mesaj ikili mi. Bir mesajin TUM parcalarinda ayni olmak zorunda. */
  readonly binary: boolean;
  /** Bu parca mesajin sonu mu. */
  readonly fin: boolean;
  /** Parcanin kendisi; bos olabilir (bos ws mesaji tek bayraktir). */
  readonly payload: Uint8Array<ArrayBuffer>;
}

function flagsOf(binary: boolean, fin: boolean): number {
  return (binary ? WS_FLAG_BINARY : 0) | (fin ? WS_FLAG_FIN : 0);
}

/**
 * ws parcasini dis cercevenin payload'ina cevirir.
 *
 * @throws {WsFrameError} parca `MAX_WS_PAYLOAD`'dan buyukse
 */
export function encodeWsPayload(
  binary: boolean,
  fin: boolean,
  payload: Uint8Array,
): Uint8Array<ArrayBuffer> {
  if (payload.byteLength > MAX_WS_PAYLOAD) {
    throw new WsFrameError(
      "ws-payload-too-large",
      `${payload.byteLength} bayt, sinir ${MAX_WS_PAYLOAD}`,
    );
  }
  const out = new Uint8Array(WS_HEADER_BYTES + payload.byteLength);
  out[0] = flagsOf(binary, fin);
  out.set(payload, WS_HEADER_BYTES);
  return out as Uint8Array<ArrayBuffer>;
}

/**
 * Dis cercevenin payload'ini ws parcasina cozer.
 *
 * Donen `payload` verilen tamponun GORUNUMUDUR (kopya degil): `decodeFrame`
 * zaten kopya uretiyor, ikinci kopya gereksiz olurdu.
 *
 * @throws {WsFrameError} payload bossa ya da bayrakta tanimsiz bit varsa
 */
export function decodeWsPayload(payload: Uint8Array<ArrayBuffer>): WsChunk {
  if (payload.byteLength < WS_HEADER_BYTES) {
    throw new WsFrameError(
      "ws-empty-frame",
      "ws cercevesinin payload'i en az 1 bayt olmali (bayrak)",
    );
  }
  const flags = payload[0] as number;
  if ((flags & ~WS_FLAG_KNOWN) !== 0) {
    throw new WsFrameError(
      "ws-bad-flags",
      `tanimsiz bit: 0x${flags.toString(16).padStart(2, "0")}`,
    );
  }
  return {
    binary: (flags & WS_FLAG_BINARY) !== 0,
    fin: (flags & WS_FLAG_FIN) !== 0,
    payload: payload.subarray(WS_HEADER_BYTES) as Uint8Array<ArrayBuffer>,
  };
}
