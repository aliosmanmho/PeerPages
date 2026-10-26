/**
 * `ctrl` kanalinin JSON mesajlari.
 *
 * Normatif tanim: `protocol/README.md`. Dogrulama ve kanonik serilestirme
 * kurallari Go tarafiyla (`agent/internal/protocol`) birebir aynidir ve
 * `protocol/testvectors.json` ile iki tarafta ayni vektorlerle test edilir.
 */

export const PROTOCOL_VERSION = 1;

/** `ctrl` hata kodlari — iki dilde ayni. */
export type CtrlErrorCode = "bad-json" | "unknown-type" | "missing-field" | "bad-field";

export class CtrlError extends Error {
  readonly code: CtrlErrorCode;

  constructor(code: CtrlErrorCode, detail: string) {
    super(`${code}: ${detail}`);
    this.name = "CtrlError";
    this.code = code;
  }
}

export type AuthMode = "none" | "required";

export interface ReqMsg {
  readonly t: "req";
  readonly id: number;
  readonly method: string;
  readonly path: string;
  readonly headers: Readonly<Record<string, string>>;
  readonly hasBody: boolean;
}

export interface ResMsg {
  readonly t: "res";
  readonly id: number;
  readonly status: number;
  readonly headers: Readonly<Record<string, string>>;
}

export interface EndMsg {
  readonly t: "end";
  readonly id: number;
}

export interface AbortMsg {
  readonly t: "abort";
  readonly id: number;
  readonly reason: string;
}

export interface PingMsg {
  readonly t: "ping";
}

export interface PongMsg {
  readonly t: "pong";
}

export interface HelloMsg {
  readonly t: "hello";
  readonly v: number;
  readonly targets: readonly string[];
  readonly auth: AuthMode;
}

export type CtrlMsg = ReqMsg | ResMsg | EndMsg | AbortMsg | PingMsg | PongMsg | HelloMsg;

const MAX_UINT32 = 0xffffffff;
const METHOD_RE = /^[A-Z]+$/;

type Raw = Record<string, unknown>;

function has(o: Raw, k: string): boolean {
  return Object.hasOwn(o, k) && o[k] !== undefined;
}

function reqField(o: Raw, k: string, t: string): unknown {
  if (!has(o, k)) {
    throw new CtrlError("missing-field", `${t}.${k} yok`);
  }
  return o[k];
}

function uint32Field(o: Raw, k: string, t: string): number {
  const v = reqField(o, k, t);
  if (typeof v !== "number" || !Number.isInteger(v) || v < 0 || v > MAX_UINT32) {
    throw new CtrlError("bad-field", `${t}.${k} uint32 olmali, verilen: ${JSON.stringify(v)}`);
  }
  return v;
}

function stringField(o: Raw, k: string, t: string): string {
  const v = reqField(o, k, t);
  if (typeof v !== "string") {
    throw new CtrlError("bad-field", `${t}.${k} string olmali, verilen: ${JSON.stringify(v)}`);
  }
  return v;
}

function nonEmptyStringField(o: Raw, k: string, t: string): string {
  const v = stringField(o, k, t);
  if (v === "") {
    throw new CtrlError("bad-field", `${t}.${k} bos olamaz`);
  }
  return v;
}

function boolField(o: Raw, k: string, t: string): boolean {
  const v = reqField(o, k, t);
  if (typeof v !== "boolean") {
    throw new CtrlError("bad-field", `${t}.${k} boolean olmali, verilen: ${JSON.stringify(v)}`);
  }
  return v;
}

function headersField(o: Raw, k: string, t: string): Record<string, string> {
  const v = reqField(o, k, t);
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    throw new CtrlError("bad-field", `${t}.${k} nesne olmali`);
  }
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(v as Raw)) {
    if (typeof value !== "string") {
      throw new CtrlError("bad-field", `${t}.${k}["${name}"] string olmali`);
    }
    // Baslik adlari kanonik bicimde kucuk harf (protocol/README.md).
    out[name.toLowerCase()] = value;
  }
  return out;
}

/**
 * Istek yolunun ilk katman dogrulamasi.
 *
 * Belirleyici katman ajandaki `internal/target.Resolve`'dur (README ilke 1);
 * burada yalnizca bariz gecersizleri reddediyoruz.
 */
function validatePath(p: string, t: string): string {
  if (p === "") {
    throw new CtrlError("bad-field", `${t}.path bos olamaz`);
  }
  if (!p.startsWith("/")) {
    throw new CtrlError("bad-field", `${t}.path "/" ile baslamali, verilen: ${JSON.stringify(p)}`);
  }
  if (p.startsWith("//")) {
    throw new CtrlError("bad-field", `${t}.path protokol-goreli olamaz`);
  }
  return p;
}

/**
 * `ctrl` kanalindan gelen metni ayristirip dogrular.
 *
 * @throws {CtrlError} `bad-json` | `unknown-type` | `missing-field` | `bad-field`
 */
export function parseCtrl(text: string): CtrlMsg {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    throw new CtrlError("bad-json", String(err));
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new CtrlError("bad-json", "en ust duzey bir JSON nesnesi olmali");
  }
  const o = raw as Raw;

  if (!has(o, "t")) {
    throw new CtrlError("missing-field", "t yok");
  }
  const t = o.t;
  if (typeof t !== "string") {
    throw new CtrlError("bad-field", `t string olmali, verilen: ${JSON.stringify(t)}`);
  }

  switch (t) {
    case "req": {
      const method = nonEmptyStringField(o, "method", t);
      if (!METHOD_RE.test(method)) {
        throw new CtrlError("bad-field", `req.method buyuk harf HTTP token'i olmali: ${method}`);
      }
      return {
        t: "req",
        id: uint32Field(o, "id", t),
        method,
        path: validatePath(stringField(o, "path", t), t),
        headers: headersField(o, "headers", t),
        hasBody: boolField(o, "hasBody", t),
      };
    }
    case "res": {
      const id = uint32Field(o, "id", t);
      const status = reqField(o, "status", t);
      if (typeof status !== "number" || !Number.isInteger(status) || status < 100 || status > 599) {
        throw new CtrlError("bad-field", `res.status 100…599 olmali, verilen: ${String(status)}`);
      }
      return { t: "res", id, status, headers: headersField(o, "headers", t) };
    }
    case "end":
      return { t: "end", id: uint32Field(o, "id", t) };
    case "abort":
      return {
        t: "abort",
        id: uint32Field(o, "id", t),
        reason: nonEmptyStringField(o, "reason", t),
      };
    case "ping":
      return { t: "ping" };
    case "pong":
      return { t: "pong" };
    case "hello": {
      const v = uint32Field(o, "v", t);
      if (v !== PROTOCOL_VERSION) {
        throw new CtrlError("bad-field", `hello.v ${PROTOCOL_VERSION} olmali, verilen: ${v}`);
      }
      const targetsRaw = reqField(o, "targets", t);
      if (!Array.isArray(targetsRaw) || targetsRaw.length === 0) {
        throw new CtrlError("bad-field", "hello.targets en az bir elemanli dizi olmali");
      }
      const targets: string[] = [];
      for (const item of targetsRaw) {
        if (typeof item !== "string" || item === "") {
          throw new CtrlError("bad-field", "hello.targets elemanlari bos olmayan string olmali");
        }
        targets.push(item);
      }
      const auth = stringField(o, "auth", t);
      if (auth !== "none" && auth !== "required") {
        throw new CtrlError("bad-field", `hello.auth "none" ya da "required" olmali: ${auth}`);
      }
      return { t: "hello", v, targets, auth };
    }
    default:
      throw new CtrlError("unknown-type", JSON.stringify(t));
  }
}

function canonicalHeaders(h: Readonly<Record<string, string>>): string {
  const keys = Object.keys(h).sort();
  const parts = keys.map((k) => `${JSON.stringify(k)}:${JSON.stringify(h[k] as string)}`);
  return `{${parts.join(",")}}`;
}

/**
 * Mesaji kanonik bicimde serilestirir: bosluksuz, tipe gore sabit anahtar
 * sirasi, `headers` kucuk harf ve sozluk siralı (protocol/README.md).
 *
 * Go tarafindaki `Canonical` ile **bayt bayt ayni** cikti uretir; paylasilan
 * test vektorleri bunu dogrular.
 */
export function canonicalCtrl(msg: CtrlMsg): string {
  switch (msg.t) {
    case "req":
      return `{"t":"req","id":${msg.id},"method":${JSON.stringify(msg.method)},"path":${JSON.stringify(
        msg.path,
      )},"headers":${canonicalHeaders(msg.headers)},"hasBody":${msg.hasBody}}`;
    case "res":
      return `{"t":"res","id":${msg.id},"status":${msg.status},"headers":${canonicalHeaders(
        msg.headers,
      )}}`;
    case "end":
      return `{"t":"end","id":${msg.id}}`;
    case "abort":
      return `{"t":"abort","id":${msg.id},"reason":${JSON.stringify(msg.reason)}}`;
    case "ping":
      return '{"t":"ping"}';
    case "pong":
      return '{"t":"pong"}';
    case "hello":
      return `{"t":"hello","v":${msg.v},"targets":[${msg.targets
        .map((x) => JSON.stringify(x))
        .join(",")}],"auth":${JSON.stringify(msg.auth)}}`;
  }
}
