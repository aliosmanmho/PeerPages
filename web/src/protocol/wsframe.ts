/**
 * The frame sub-format of the WebSocket bridge (phase 6).
 *
 * The outer frame (`[4B id][4B seq][payload]`, `framing.ts`) is kept
 * EXACTLY as is: the ws payload uses neither a separate channel nor a
 * separate framing. The PAYLOAD of a frame belonging to a ws id looks like
 * this:
 *
 *     [1B flags][chunk]
 *
 * That way backpressure, the seq check and 16 KiB chunking come for free;
 * binary data takes no base64 inflation, and bulk ws traffic does not clog
 * the `ctrl` channel (ping, abort).
 *
 * Normative definition: `protocol/README.md`. Go counterpart:
 * `agent/internal/protocol/wsframe.go`; both sides read the same vectors.
 */

import { MAX_PAYLOAD } from "./framing";

/** Flag: 1 means a binary message, 0 a text one. */
export const WS_FLAG_BINARY = 0x01;

/** Flag: 1 means this is the LAST chunk of the ws message. */
export const WS_FLAG_FIN = 0x02;

/** The defined bits; any bit outside them is a protocol error. */
export const WS_FLAG_KNOWN = WS_FLAG_BINARY | WS_FLAG_FIN;

/** The ws sub-header: just the flag byte. */
export const WS_HEADER_BYTES = 1;

/** Largest size of a single ws chunk. */
export const MAX_WS_PAYLOAD = MAX_PAYLOAD - WS_HEADER_BYTES;

/** ws frame error codes — the same in both languages. */
export type WsFrameErrorCode = "ws-empty-frame" | "ws-bad-flags" | "ws-payload-too-large";

/** A ws frame error. `code` is the same in both languages; testvectors.json checks against it. */
export class WsFrameError extends Error {
  readonly code: WsFrameErrorCode;

  constructor(code: WsFrameErrorCode, detail: string) {
    super(`${code}: ${detail}`);
    this.name = "WsFrameError";
    this.code = code;
  }
}

export interface WsChunk {
  /** Whether the message is binary. Must be the same on ALL chunks of a message. */
  readonly binary: boolean;
  /** Whether this chunk ends the message. */
  readonly fin: boolean;
  /** The chunk itself; may be empty (an empty ws message is just flags). */
  readonly payload: Uint8Array<ArrayBuffer>;
}

function flagsOf(binary: boolean, fin: boolean): number {
  return (binary ? WS_FLAG_BINARY : 0) | (fin ? WS_FLAG_FIN : 0);
}

/**
 * Turns a ws chunk into the outer frame's payload.
 *
 * @throws {WsFrameError} when the chunk is larger than `MAX_WS_PAYLOAD`
 */
export function encodeWsPayload(
  binary: boolean,
  fin: boolean,
  payload: Uint8Array,
): Uint8Array<ArrayBuffer> {
  if (payload.byteLength > MAX_WS_PAYLOAD) {
    throw new WsFrameError(
      "ws-payload-too-large",
      `${payload.byteLength} bytes, limit ${MAX_WS_PAYLOAD}`,
    );
  }
  const out = new Uint8Array(WS_HEADER_BYTES + payload.byteLength);
  out[0] = flagsOf(binary, fin);
  out.set(payload, WS_HEADER_BYTES);
  return out as Uint8Array<ArrayBuffer>;
}

/**
 * Decodes the outer frame's payload into a ws chunk.
 *
 * The returned `payload` is a VIEW of the given buffer (not a copy):
 * `decodeFrame` already made a copy, a second one would be pointless.
 *
 * @throws {WsFrameError} when the payload is empty or the flags carry an
 * undefined bit
 */
export function decodeWsPayload(payload: Uint8Array<ArrayBuffer>): WsChunk {
  if (payload.byteLength < WS_HEADER_BYTES) {
    throw new WsFrameError(
      "ws-empty-frame",
      "the payload of a ws frame must be at least 1 byte (the flags)",
    );
  }
  const flags = payload[0] as number;
  if ((flags & ~WS_FLAG_KNOWN) !== 0) {
    throw new WsFrameError(
      "ws-bad-flags",
      `undefined bit: 0x${flags.toString(16).padStart(2, "0")}`,
    );
  }
  return {
    binary: (flags & WS_FLAG_BINARY) !== 0,
    fin: (flags & WS_FLAG_FIN) !== 0,
    payload: payload.subarray(WS_HEADER_BYTES) as Uint8Array<ArrayBuffer>,
  };
}
