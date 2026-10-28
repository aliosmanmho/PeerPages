/**
 * The binary frame format of the `data` channel.
 *
 * `[4B id BE][4B seq BE][payload <= 16384]` — normative definition:
 * `protocol/README.md`. The test vectors live in
 * `protocol/testvectors.json` and the Go side
 * (`agent/internal/protocol`) reads the same file.
 */

/** Frame header: id (4) + seq (4). */
export const HEADER_BYTES = 8;

/** Largest payload: 16 KiB (browser data channel compatibility, BRIEF section 3). */
export const MAX_PAYLOAD = 16384;

/** Largest frame: header + payload. */
export const MAX_FRAME = HEADER_BYTES + MAX_PAYLOAD;

/** Framing error codes shared across the protocol. */
export type FramingErrorCode = "short-frame" | "payload-too-large";

/** A framing error. `code` is the same in both languages; testvectors.json checks against it. */
export class FramingError extends Error {
  readonly code: FramingErrorCode;

  constructor(code: FramingErrorCode, detail: string) {
    super(`${code}: ${detail}`);
    this.name = "FramingError";
    this.code = code;
  }
}

export interface Frame {
  readonly id: number;
  readonly seq: number;
  /**
   * A chunk of the body. The buffer type is fixed to `ArrayBuffer`: we
   * always build the frame in a fresh buffer, so it can go straight into
   * APIs that do not accept a SharedArrayBuffer, such as
   * `RTCDataChannel.send` and the `postMessage` transfer list.
   */
  readonly payload: Uint8Array<ArrayBuffer>;
}

function assertUint32(name: string, v: number): void {
  if (!Number.isInteger(v) || v < 0 || v > 0xffffffff) {
    throw new RangeError(`${name} must be a uint32 (0...4294967295), got: ${v}`);
  }
}

/**
 * Encodes a frame.
 *
 * @throws {FramingError} when the payload is larger than 16 KiB (`payload-too-large`)
 * @throws {RangeError} when id or seq is outside the uint32 range
 */
export function encodeFrame(id: number, seq: number, payload: Uint8Array): Uint8Array<ArrayBuffer> {
  assertUint32("id", id);
  assertUint32("seq", seq);
  if (payload.byteLength > MAX_PAYLOAD) {
    throw new FramingError(
      "payload-too-large",
      `${payload.byteLength} bytes, limit ${MAX_PAYLOAD}`,
    );
  }

  const out = new Uint8Array(HEADER_BYTES + payload.byteLength);
  const view = new DataView(out.buffer);
  view.setUint32(0, id, false);
  view.setUint32(4, seq, false);
  out.set(payload, HEADER_BYTES);
  return out;
}

/**
 * Decodes a frame. The returned `payload` is a **copy** of the frame
 * buffer; we never hold on to the buffer the data channel handed us (the
 * browser may reuse it).
 *
 * @throws {FramingError} `short-frame` or `payload-too-large`
 */
export function decodeFrame(frame: Uint8Array): Frame {
  if (frame.byteLength < HEADER_BYTES) {
    throw new FramingError(
      "short-frame",
      `${frame.byteLength} bytes, at least ${HEADER_BYTES} required`,
    );
  }
  const payloadLen = frame.byteLength - HEADER_BYTES;
  if (payloadLen > MAX_PAYLOAD) {
    throw new FramingError("payload-too-large", `${payloadLen} bytes, limit ${MAX_PAYLOAD}`);
  }

  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
  return {
    id: view.getUint32(0, false),
    seq: view.getUint32(4, false),
    payload: frame.slice(HEADER_BYTES),
  };
}

/**
 * Splits the body into 16 KiB chunks and yields a frame for each.
 * `seq` starts at the given value and increments.
 */
export function* frameBody(
  id: number,
  body: Uint8Array,
  startSeq = 0,
): Generator<Uint8Array<ArrayBuffer>> {
  assertUint32("id", id);
  assertUint32("startSeq", startSeq);

  if (body.byteLength === 0) {
    return;
  }
  let seq = startSeq;
  for (let off = 0; off < body.byteLength; off += MAX_PAYLOAD) {
    const end = Math.min(off + MAX_PAYLOAD, body.byteLength);
    yield encodeFrame(id, seq, body.subarray(off, end));
    seq += 1;
  }
}
