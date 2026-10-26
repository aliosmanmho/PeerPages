/**
 * `data` kanalinin ikili cerceve bicimi.
 *
 * `[4B id BE][4B seq BE][payload <= 16384]` — normatif tanim:
 * `protocol/README.md`. Test vektorleri `protocol/testvectors.json` icinde ve
 * Go tarafi (`agent/internal/protocol`) ayni dosyayi okur.
 */

/** Cerceve basligi: id (4) + seq (4). */
export const HEADER_BYTES = 8;

/** En buyuk payload: 16 KiB (tarayici data channel uyumlulugu, README §3). */
export const MAX_PAYLOAD = 16384;

/** En buyuk cerceve: baslik + payload. */
export const MAX_FRAME = HEADER_BYTES + MAX_PAYLOAD;

/** Protokol genelinde paylasilan framing hata kodlari. */
export type FramingErrorCode = "short-frame" | "payload-too-large";

/** Framing hatasi. `code` iki dilde aynidir; testvectors.json buna gore dogrular. */
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
  readonly payload: Uint8Array;
}

function assertUint32(name: string, v: number): void {
  if (!Number.isInteger(v) || v < 0 || v > 0xffffffff) {
    throw new RangeError(`${name} uint32 olmali (0…4294967295), verilen: ${v}`);
  }
}

/**
 * Cerceveyi kodlar.
 *
 * @throws {FramingError} payload 16 KiB'dan buyukse (`payload-too-large`)
 * @throws {RangeError} id ya da seq uint32 araliginda degilse
 */
export function encodeFrame(id: number, seq: number, payload: Uint8Array): Uint8Array {
  assertUint32("id", id);
  assertUint32("seq", seq);
  if (payload.byteLength > MAX_PAYLOAD) {
    throw new FramingError("payload-too-large", `${payload.byteLength} bayt, sinir ${MAX_PAYLOAD}`);
  }

  const out = new Uint8Array(HEADER_BYTES + payload.byteLength);
  const view = new DataView(out.buffer);
  view.setUint32(0, id, false);
  view.setUint32(4, seq, false);
  out.set(payload, HEADER_BYTES);
  return out;
}

/**
 * Cerceveyi cozer. Donen `payload` cerceve tamponunun **kopyasidir**; data
 * channel'in verdigi tamponu tutmayiz (tarayici onu yeniden kullanabilir).
 *
 * @throws {FramingError} `short-frame` ya da `payload-too-large`
 */
export function decodeFrame(frame: Uint8Array): Frame {
  if (frame.byteLength < HEADER_BYTES) {
    throw new FramingError(
      "short-frame",
      `${frame.byteLength} bayt, en az ${HEADER_BYTES} gerekli`,
    );
  }
  const payloadLen = frame.byteLength - HEADER_BYTES;
  if (payloadLen > MAX_PAYLOAD) {
    throw new FramingError("payload-too-large", `${payloadLen} bayt, sinir ${MAX_PAYLOAD}`);
  }

  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
  return {
    id: view.getUint32(0, false),
    seq: view.getUint32(4, false),
    payload: frame.slice(HEADER_BYTES),
  };
}

/**
 * Gövdeyi 16 KiB'lik parcalara boler ve her biri icin cerceve uretir.
 * `seq` verilen degerden baslayarak artar.
 */
export function* frameBody(id: number, body: Uint8Array, startSeq = 0): Generator<Uint8Array> {
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
