/**
 * Nostr uzerinden tasinan signaling mesajlari.
 *
 * Normatif tanim: `protocol/signaling.md`. Go karsiligi:
 * `agent/internal/signaling`. Iki taraf AYNI test vektorlerini okur:
 * `protocol/signaling-testvectors.json` (ucuncu bir bagimsiz uygulama ile
 * uretildi).
 *
 * Bu dosya YALNIZCA mesaj bicimini bilir; Nostr tasimasi `./pool.ts`'de.
 */

import { chacha20poly1305 } from "@noble/ciphers/chacha.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { base64urlnopad } from "@scure/base";

/** Su an desteklenen signaling surumu. */
export const SIGNALING_VERSION = 1;

/** Sinirlar (protocol/signaling.md). */
export const SID_LENGTH = 16;
export const MAX_SDP = 65536;
export const MAX_CANDIDATE = 1024;
export const MAX_MID = 32;
export const MAX_REASON = 64;
export const MAX_TARGET_NAME = 32;

/**
 * Hedef adi bicimi (protocol/signaling.md).
 *
 * Go tarafindaki targetNameRe ve agent/internal/target'taki nameRe ile AYNI
 * kumeyi kabul etmek zorunda; paylasilan vektorler bunu dogruluyor.
 */
const TARGET_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$/;

/** Hedef adi bicimini dogrular. */
export function validTargetName(name: string): boolean {
  return TARGET_NAME_RE.test(name);
}

/** Paylasilan sir katmani parametreleri. */
export const SECRET_PREFIX = "pps1.";
const SECRET_SALT = "peerpages-secret-v1";
const SECRET_INFO = "signaling";
const SECRET_AAD = "peerpages-signaling-v1";
const NONCE_BYTES = 12;
const AEAD_TAG_BYTES = 16;
const KEY_BYTES = 32;

export type SignalingType = "offer" | "answer" | "candidate" | "bye";

/** Hata kodlari — data channel protokolunun kumesi + bad-secret. */
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
   * Ziyaretcinin istedigi hedef adi (URL'deki `#npub1…/admin`).
   *
   * Opsiyonel: hedef belirtilmediginde alan kanonik bicime HIC yazilmaz
   * (ajan `default`'a duser).
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

/** Yeni oturum kimligi uretir (16 kucuk harf hex = 64 bit). */
export function newSID(): string {
  const bytes = new Uint8Array(SID_LENGTH / 2);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) {
    out += b.toString(16).padStart(2, "0");
  }
  return out;
}

/** sid bicimini dogrular: tam 16 kucuk harf hex. */
export function validSID(sid: string): boolean {
  return sid.length === SID_LENGTH && /^[0-9a-f]+$/.test(sid);
}

/**
 * Mesajin kanonik serilestirmesi (protocol/signaling.md).
 *
 * Go tarafindaki Msg.Canonical ile bayt bayt ayni cikti uretir:
 * bosluk yok, anahtar sirasi tipe gore sabit.
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

/** Alan kurallarini dogrular; gecersizse SignalingError atar. */
export function validateSignaling(msg: SignalingMsg): void {
  if (!validSID(msg.sid)) {
    throw new SignalingError(
      "bad-field",
      `sid tam ${SID_LENGTH} kucuk harf hex olmali, verilen: ${JSON.stringify(msg.sid)}`,
    );
  }
  switch (msg.t) {
    case "offer":
      checkLen("offer.sdp", msg.sdp, MAX_SDP);
      if (msg.target !== undefined && !validTargetName(msg.target)) {
        throw new SignalingError(
          "bad-field",
          `offer.target bicimi gecersiz, verilen: ${JSON.stringify(msg.target)}`,
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
 * Uzunluk kontrolu BAYT cinsinden yapilir.
 *
 * Go tarafi `len(string)` ile bayt sayiyor; JS'in `.length`'i UTF-16 kod
 * birimi sayar. ASCII disi karakterlerde ikisi farkli olur, bu yuzden
 * TextEncoder ile bayta ceviriyoruz — aksi halde iki taraf ayni mesaj icin
 * farkli karar verir.
 */
function checkLen(label: string, value: string, max: number): void {
  if (value === "") {
    throw new SignalingError("bad-field", `${label} bos olamaz`);
  }
  const bytes = enc.encode(value).byteLength;
  if (bytes > max) {
    throw new SignalingError(
      "bad-field",
      `${label} en fazla ${max} bayt olabilir, verilen: ${bytes}`,
    );
  }
}

type Raw = Record<string, unknown>;

/**
 * Kanonik JSON metnini ayristirip dogrular.
 *
 * Sirli payload'lar icin once `Sealer.open` cagrilmali; bu fonksiyon duz
 * JSON bekler.
 */
export function parseSignaling(text: string): SignalingMsg {
  const trimmed = text.replace(/^[\s]+/, "");
  if (trimmed === "") {
    throw new SignalingError("bad-json", "bos metin");
  }
  if (!trimmed.startsWith("{")) {
    throw new SignalingError("bad-json", "en ust duzey bir JSON nesnesi olmali");
  }

  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    throw new SignalingError("bad-json", String(err));
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new SignalingError("bad-json", "en ust duzey bir JSON nesnesi olmali");
  }
  const o = raw as Raw;

  // Sira protocol/signaling.md'de tanimli.
  if (!has(o, "t")) {
    throw new SignalingError("missing-field", "t yok");
  }
  const t = o.t;
  if (typeof t !== "string") {
    throw new SignalingError("bad-field", `t string olmali, verilen: ${JSON.stringify(t)}`);
  }

  if (!has(o, "v")) {
    throw new SignalingError("missing-field", "v yok");
  }
  const v = o.v;
  if (typeof v !== "number" || !Number.isInteger(v)) {
    throw new SignalingError("bad-field", `v tamsayi olmali, verilen: ${JSON.stringify(v)}`);
  }
  if (v !== SIGNALING_VERSION) {
    throw new SignalingError("bad-field", `v ${SIGNALING_VERSION} olmali, verilen: ${v}`);
  }

  const sid = stringField(o, "sid");
  if (!validSID(sid)) {
    throw new SignalingError(
      "bad-field",
      `sid tam ${SID_LENGTH} kucuk harf hex olmali, verilen: ${JSON.stringify(sid)}`,
    );
  }

  let msg: SignalingMsg;
  switch (t) {
    case "offer": {
      const sdp = stringField(o, "sdp");
      // target opsiyonel: alanin HIC olmamasi gecerli, ama varsa gecerli bir
      // ad olmak zorunda (bos string ya da null degil).
      if (Object.hasOwn(o, "target") && o.target !== undefined) {
        const name = o.target;
        if (typeof name !== "string") {
          throw new SignalingError(
            "bad-field",
            `target string olmali, verilen: ${JSON.stringify(name)}`,
          );
        }
        if (name === "") {
          throw new SignalingError(
            "bad-field",
            "offer.target bos olamaz (hedefsiz offer'da alan hic olmamali)",
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
    throw new SignalingError("missing-field", `${key} yok`);
  }
  const v = o[key];
  if (typeof v !== "string") {
    throw new SignalingError("bad-field", `${key} string olmali, verilen: ${JSON.stringify(v)}`);
  }
  return v;
}

/**
 * Paylasilan sir katmani.
 *
 * `null` Sealer "sir yok" demektir ve payload duz kanonik JSON olur.
 * Kurallar (protocol/signaling.md):
 *  - Sir varsa payload sirli OLMAK ZORUNDA; duz JSON reddedilir.
 *  - Sir yoksa sirli payload reddedilir (cozemeyiz).
 */
export class Sealer {
  private readonly key: Uint8Array;

  private constructor(key: Uint8Array) {
    this.key = key;
  }

  /** Sirdan HKDF-SHA256 ile anahtar turetir. */
  static fromSecret(secret: string): Sealer {
    if (secret === "") {
      throw new SignalingError("bad-secret", "paylasilan sir bos");
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

  /** Turetilen anahtari hex olarak dondurur. YALNIZCA test icin. */
  keyHex(): string {
    let out = "";
    for (const b of this.key) {
      out += b.toString(16).padStart(2, "0");
    }
    return out;
  }

  /** Kanonik JSON'u sirli payload'a cevirir. */
  seal(canonical: string): string {
    const nonce = new Uint8Array(NONCE_BYTES);
    crypto.getRandomValues(nonce);
    return this.sealWithNonce(nonce, canonical);
  }

  /**
   * Sabit nonce ile sifreler. Test vektorleri icin; uretimde `seal`
   * kullanilir (nonce her mesajda rastgele olmali).
   */
  sealWithNonce(nonce: Uint8Array, canonical: string): string {
    if (nonce.byteLength !== NONCE_BYTES) {
      throw new SignalingError(
        "bad-secret",
        `nonce ${NONCE_BYTES} bayt olmali, verilen: ${nonce.byteLength}`,
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

  /** Sirli payload'i kanonik JSON'a cevirir. */
  open(payload: string): string {
    if (!isSealed(payload)) {
      throw new SignalingError("bad-secret", "paylasilan sir gerekli ama payload sirsiz");
    }
    const body = payload.slice(SECRET_PREFIX.length);

    let blob: Uint8Array;
    try {
      blob = base64urlnopad.decode(body);
    } catch {
      throw new SignalingError("bad-secret", "base64 cozulemedi");
    }
    if (blob.byteLength < NONCE_BYTES + AEAD_TAG_BYTES) {
      throw new SignalingError("bad-secret", `payload cok kisa (${blob.byteLength} bayt)`);
    }

    const nonce = blob.subarray(0, NONCE_BYTES);
    const ct = blob.subarray(NONCE_BYTES);
    try {
      const plain = chacha20poly1305(this.key, nonce, enc.encode(SECRET_AAD)).decrypt(ct);
      return dec.decode(plain);
    } catch {
      // Yanlis sir, bozuk etiket ya da oynanmis veri: ayrim yapmiyoruz.
      throw new SignalingError("bad-secret", "payload dogrulanamadi (yanlis sir ya da bozuk veri)");
    }
  }

  /** Mesaji payload'a cevirir. */
  encode(msg: SignalingMsg): string {
    validateSignaling(msg);
    return this.seal(canonicalSignaling(msg));
  }

  /** Payload'i mesaja cevirir. */
  decode(payload: string): SignalingMsg {
    return parseSignaling(this.open(payload));
  }
}

/** Payload sirli mi. */
export function isSealed(payload: string): boolean {
  return payload.startsWith(SECRET_PREFIX);
}

/**
 * Sir olmadan kodlama: payload duz kanonik JSON.
 *
 * `Sealer` ile ayni sozlesmeyi paylasir ki cagiran taraf sir olup olmadigini
 * tek bir yerde kararlastirip gerisini ayni sekilde yazabilsin.
 */
export const plainCodec = {
  encode(msg: SignalingMsg): string {
    validateSignaling(msg);
    return canonicalSignaling(msg);
  },
  decode(payload: string): SignalingMsg {
    if (isSealed(payload)) {
      throw new SignalingError("bad-secret", "payload sirli ama paylasilan sir verilmedi");
    }
    return parseSignaling(payload);
  },
};

/** Sir varsa Sealer, yoksa duz codec. */
export type Codec = Pick<Sealer, "encode" | "decode">;

export function codecFor(secret: string | null): Codec {
  return secret ? Sealer.fromSecret(secret) : plainCodec;
}
