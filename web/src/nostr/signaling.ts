/**
 * The signaling messages carried over Nostr.
 *
 * Normative definition: `protocol/signaling.md`. Go counterpart:
 * `agent/internal/signaling`. Both sides read the SAME test vectors:
 * `protocol/signaling-testvectors.json` (produced with a third,
 * independent implementation).
 *
 * This file knows ONLY the message format; the Nostr transport lives in
 * `./pool.ts`.
 */

import { chacha20poly1305 } from "@noble/ciphers/chacha.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { base64urlnopad } from "@scure/base";

/** The signaling version supported right now. */
export const SIGNALING_VERSION = 1;

/** Limits (protocol/signaling.md). */
export const SID_LENGTH = 16;
export const MAX_SDP = 65536;
export const MAX_CANDIDATE = 1024;
export const MAX_MID = 32;
export const MAX_REASON = 64;
export const MAX_TARGET_NAME = 32;

/**
 * Target name format (protocol/signaling.md).
 *
 * It MUST accept the same set as targetNameRe on the Go side and nameRe in
 * agent/internal/target; the shared vectors check that.
 */
const TARGET_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$/;

/** Validates the target name format. */
export function validTargetName(name: string): boolean {
  return TARGET_NAME_RE.test(name);
}

/** Parameters of the shared-secret layer. */
export const SECRET_PREFIX = "pps1.";
const SECRET_SALT = "peerpages-secret-v1";
const SECRET_INFO = "signaling";
const SECRET_AAD = "peerpages-signaling-v1";
const NONCE_BYTES = 12;
const AEAD_TAG_BYTES = 16;
const KEY_BYTES = 32;

export type SignalingType = "offer" | "answer" | "candidate" | "bye";

/** Error codes: the data channel protocol's set plus bad-secret. */
export type SignalingErrorCode =
  | "bad-secret"
  | "bad-json"
  | "unknown-type"
  | "missing-field"
  | "bad-field";

export class SignalingError extends Error {
  readonly code: SignalingErrorCode;

  constructor(code: SignalingErrorCode, detail: string) {
    super(`${code}: ${detail}`);
    this.name = "SignalingError";
    this.code = code;
  }
}

export interface OfferMsg {
  readonly t: "offer";
  readonly sid: string;
  readonly sdp: string;
  /**
   * The target name the visitor asked for (`#npub1.../admin` in the URL).
   *
   * Optional: with no target given, the field is NOT written into the
   * canonical form at all (the agent falls back to `default`).
   */
  readonly target?: string;
}

export interface AnswerMsg {
  readonly t: "answer";
  readonly sid: string;
  readonly sdp: string;
}

export interface CandidateMsg {
  readonly t: "candidate";
  readonly sid: string;
  readonly cand: string;
  readonly mid: string;
}

export interface ByeMsg {
  readonly t: "bye";
  readonly sid: string;
  readonly reason: string;
}

export type SignalingMsg = OfferMsg | AnswerMsg | CandidateMsg | ByeMsg;

const enc = new TextEncoder();
const dec = new TextDecoder();

/** Generates a new session id (16 lowercase hex chars = 64 bits). */
export function newSID(): string {
  const bytes = new Uint8Array(SID_LENGTH / 2);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) {
    out += b.toString(16).padStart(2, "0");
  }
  return out;
}

/** Validates the sid format: exactly 16 lowercase hex chars. */
export function validSID(sid: string): boolean {
  return sid.length === SID_LENGTH && /^[0-9a-f]+$/.test(sid);
}

/**
 * The message's canonical serialization (protocol/signaling.md).
 *
 * Produces byte-for-byte the same output as Msg.Canonical on the Go side:
 * no whitespace, key order fixed per type.
 */
export function canonicalSignaling(msg: SignalingMsg): string {
  const head = `{"v":${SIGNALING_VERSION},"t":${JSON.stringify(msg.t)},"sid":${JSON.stringify(msg.sid)}`;
  switch (msg.t) {
    case "offer": {
      const sdp = `${head},"sdp":${JSON.stringify(msg.sdp)}`;
      return msg.target ? `${sdp},"target":${JSON.stringify(msg.target)}}` : `${sdp}}`;
    }
    case "answer":
      return `${head},"sdp":${JSON.stringify(msg.sdp)}}`;
    case "candidate":
      return `${head},"cand":${JSON.stringify(msg.cand)},"mid":${JSON.stringify(msg.mid)}}`;
    case "bye":
      return `${head},"reason":${JSON.stringify(msg.reason)}}`;
  }
}

/** Validates the field rules; throws SignalingError when invalid. */
export function validateSignaling(msg: SignalingMsg): void {
  if (!validSID(msg.sid)) {
    throw new SignalingError(
      "bad-field",
      `sid must be exactly ${SID_LENGTH} lowercase hex chars, got: ${JSON.stringify(msg.sid)}`,
    );
  }
  switch (msg.t) {
    case "offer":
      checkLen("offer.sdp", msg.sdp, MAX_SDP);
      if (msg.target !== undefined && !validTargetName(msg.target)) {
        throw new SignalingError(
          "bad-field",
          `offer.target format is invalid, got: ${JSON.stringify(msg.target)}`,
        );
      }
      return;
    case "answer":
      checkLen("answer.sdp", msg.sdp, MAX_SDP);
      return;
    case "candidate":
      checkLen("candidate.cand", msg.cand, MAX_CANDIDATE);
      checkLen("candidate.mid", msg.mid, MAX_MID);
      return;
    case "bye":
      checkLen("bye.reason", msg.reason, MAX_REASON);
      return;
  }
}

/**
 * The length check is done in BYTES.
 *
 * The Go side counts bytes with `len(string)`; JS `.length` counts UTF-16
 * code units. For non-ASCII characters the two differ, so we convert with
 * TextEncoder — otherwise the two sides would decide differently about the
 * same message.
 */
function checkLen(label: string, value: string, max: number): void {
  if (value === "") {
    throw new SignalingError("bad-field", `${label} must not be empty`);
  }
  const bytes = enc.encode(value).byteLength;
  if (bytes > max) {
    throw new SignalingError("bad-field", `${label} may be at most ${max} bytes, got: ${bytes}`);
  }
}

type Raw = Record<string, unknown>;

/**
 * Parses and validates canonical JSON text.
 *
 * For sealed payloads `Sealer.open` must be called first; this function
 * expects plain JSON.
 */
export function parseSignaling(text: string): SignalingMsg {
  const trimmed = text.replace(/^[\s]+/, "");
  if (trimmed === "") {
    throw new SignalingError("bad-json", "empty text");
  }
  if (!trimmed.startsWith("{")) {
    throw new SignalingError("bad-json", "the top level must be a JSON object");
  }

  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    throw new SignalingError("bad-json", String(err));
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new SignalingError("bad-json", "the top level must be a JSON object");
  }
  const o = raw as Raw;

  // The order is defined in protocol/signaling.md.
  if (!has(o, "t")) {
    throw new SignalingError("missing-field", "t is missing");
  }
  const t = o.t;
  if (typeof t !== "string") {
    throw new SignalingError("bad-field", `t must be a string, got: ${JSON.stringify(t)}`);
  }

  if (!has(o, "v")) {
    throw new SignalingError("missing-field", "v is missing");
  }
  const v = o.v;
  if (typeof v !== "number" || !Number.isInteger(v)) {
    throw new SignalingError("bad-field", `v must be an integer, got: ${JSON.stringify(v)}`);
  }
  if (v !== SIGNALING_VERSION) {
    throw new SignalingError("bad-field", `v must be ${SIGNALING_VERSION}, got: ${v}`);
  }

  const sid = stringField(o, "sid");
  if (!validSID(sid)) {
    throw new SignalingError(
      "bad-field",
      `sid must be exactly ${SID_LENGTH} lowercase hex chars, got: ${JSON.stringify(sid)}`,
    );
  }

  let msg: SignalingMsg;
  switch (t) {
    case "offer": {
      const sdp = stringField(o, "sdp");
      // target is optional: the field being absent entirely is valid, but
      // if present it must be a valid name (not an empty string or null).
      if (Object.hasOwn(o, "target") && o.target !== undefined) {
        const name = o.target;
        if (typeof name !== "string") {
          throw new SignalingError(
            "bad-field",
            `target must be a string, got: ${JSON.stringify(name)}`,
          );
        }
        if (name === "") {
          throw new SignalingError(
            "bad-field",
            "offer.target must not be empty (in a target-less offer the field must be absent)",
          );
        }
        msg = { t: "offer", sid, sdp, target: name };
        break;
      }
      msg = { t: "offer", sid, sdp };
      break;
    }
    case "answer":
      msg = { t: "answer", sid, sdp: stringField(o, "sdp") };
      break;
    case "candidate":
      msg = { t: "candidate", sid, cand: stringField(o, "cand"), mid: stringField(o, "mid") };
      break;
    case "bye":
      msg = { t: "bye", sid, reason: stringField(o, "reason") };
      break;
    default:
      throw new SignalingError("unknown-type", JSON.stringify(t));
  }

  validateSignaling(msg);
  return msg;
}

function has(o: Raw, k: string): boolean {
  return Object.hasOwn(o, k) && o[k] !== undefined;
}

function stringField(o: Raw, key: string): string {
  if (!has(o, key)) {
    throw new SignalingError("missing-field", `${key} is missing`);
  }
  const v = o[key];
  if (typeof v !== "string") {
    throw new SignalingError("bad-field", `${key} must be a string, got: ${JSON.stringify(v)}`);
  }
  return v;
}

/**
 * The shared-secret layer.
 *
 * A `null` Sealer means "no secret" and the payload is plain canonical
 * JSON. The rules (protocol/signaling.md):
 *  - with a secret the payload MUST be sealed; plain JSON is rejected,
 *  - without a secret a sealed payload is rejected (we cannot open it).
 */
export class Sealer {
  private readonly key: Uint8Array;

  private constructor(key: Uint8Array) {
    this.key = key;
  }

  /** Derives the key from the secret with HKDF-SHA256. */
  static fromSecret(secret: string): Sealer {
    if (secret === "") {
      throw new SignalingError("bad-secret", "the shared secret is empty");
    }
    const key = hkdf(
      sha256,
      enc.encode(secret),
      enc.encode(SECRET_SALT),
      enc.encode(SECRET_INFO),
      KEY_BYTES,
    );
    return new Sealer(key);
  }

  /** Returns the derived key as hex. For tests ONLY. */
  keyHex(): string {
    let out = "";
    for (const b of this.key) {
      out += b.toString(16).padStart(2, "0");
    }
    return out;
  }

  /** Turns canonical JSON into a sealed payload. */
  seal(canonical: string): string {
    const nonce = new Uint8Array(NONCE_BYTES);
    crypto.getRandomValues(nonce);
    return this.sealWithNonce(nonce, canonical);
  }

  /**
   * Encrypts with a fixed nonce. For test vectors; production uses `seal`
   * (the nonce must be random for every message).
   */
  sealWithNonce(nonce: Uint8Array, canonical: string): string {
    if (nonce.byteLength !== NONCE_BYTES) {
      throw new SignalingError(
        "bad-secret",
        `nonce must be ${NONCE_BYTES} bytes, got: ${nonce.byteLength}`,
      );
    }
    const ct = chacha20poly1305(this.key, nonce, enc.encode(SECRET_AAD)).encrypt(
      enc.encode(canonical),
    );
    const blob = new Uint8Array(nonce.byteLength + ct.byteLength);
    blob.set(nonce, 0);
    blob.set(ct, nonce.byteLength);
    return SECRET_PREFIX + base64urlnopad.encode(blob);
  }

  /** Turns a sealed payload back into canonical JSON. */
  open(payload: string): string {
    if (!isSealed(payload)) {
      throw new SignalingError(
        "bad-secret",
        "a shared secret is required but the payload is not sealed",
      );
    }
    const body = payload.slice(SECRET_PREFIX.length);

    let blob: Uint8Array;
    try {
      blob = base64urlnopad.decode(body);
    } catch {
      throw new SignalingError("bad-secret", "base64 could not be decoded");
    }
    if (blob.byteLength < NONCE_BYTES + AEAD_TAG_BYTES) {
      throw new SignalingError("bad-secret", `payload is too short (${blob.byteLength} bytes)`);
    }

    const nonce = blob.subarray(0, NONCE_BYTES);
    const ct = blob.subarray(NONCE_BYTES);
    try {
      const plain = chacha20poly1305(this.key, nonce, enc.encode(SECRET_AAD)).decrypt(ct);
      return dec.decode(plain);
    } catch {
      // Wrong secret, broken tag or tampered data: we do not distinguish.
      throw new SignalingError(
        "bad-secret",
        "the payload could not be verified (wrong secret or corrupt data)",
      );
    }
  }

  /** Turns a message into a payload. */
  encode(msg: SignalingMsg): string {
    validateSignaling(msg);
    return this.seal(canonicalSignaling(msg));
  }

  /** Turns a payload into a message. */
  decode(payload: string): SignalingMsg {
    return parseSignaling(this.open(payload));
  }
}

/** Whether the payload is sealed. */
export function isSealed(payload: string): boolean {
  return payload.startsWith(SECRET_PREFIX);
}

/**
 * Encoding without a secret: the payload is plain canonical JSON.
 *
 * It shares the same contract as `Sealer` so the caller decides in one
 * place whether there is a secret and writes the rest the same way.
 */
export const plainCodec = {
  encode(msg: SignalingMsg): string {
    validateSignaling(msg);
    return canonicalSignaling(msg);
  },
  decode(payload: string): SignalingMsg {
    if (isSealed(payload)) {
      throw new SignalingError(
        "bad-secret",
        "the payload is sealed but no shared secret was given",
      );
    }
    return parseSignaling(payload);
  },
};

/** Sealer when there is a secret, the plain codec otherwise. */
export type Codec = Pick<Sealer, "encode" | "decode">;

export function codecFor(secret: string | null): Codec {
  return secret ? Sealer.fromSecret(secret) : plainCodec;
}
