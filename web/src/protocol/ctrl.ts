/**
 * The JSON messages of the `ctrl` channel.
 *
 * Normative definition: `protocol/README.md`. The validation and canonical
 * serialization rules are identical to the Go side
 * (`agent/internal/protocol`), and both sides are tested against the same
 * vectors in `protocol/testvectors.json`.
 */

export const PROTOCOL_VERSION = 1;

/** `ctrl` error codes — the same in both languages. */
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
  /**
   * Number of body frames sent in this direction.
   *
   * Needed because `ctrl` and `data` are separate SCTP streams: `end` can
   * arrive BEFORE the body frames. The receiver keeps listening on `data`
   * until it reaches this count (protocol/README.md).
   */
  readonly frames: number;
  /** Total byte count of the body. */
  readonly len: number;
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

/**
 * A WebSocket open request (phase 6), visitor to agent.
 *
 * The payload travels on the `data` channel (`wsframe.ts`); this message
 * is only the handshake. `id` comes from the SAME id space as HTTP
 * requests.
 */
export interface WsOpenMsg {
  readonly t: "ws-open";
  readonly id: number;
  readonly path: string;
  /** Requested subprotocols; may be empty. */
  readonly protos: readonly string[];
}

/** A WebSocket connection the agent accepted. */
export interface WsOkMsg {
  readonly t: "ws-ok";
  readonly id: number;
  /** The subprotocol the server chose; empty string when there is none. */
  readonly proto: string;
}

/**
 * Reports that a WebSocket session closed (in either direction).
 *
 * HTTP's end/abort pair is not used for ws: the code and reason go
 * straight into the visitor's `onclose`.
 */
export interface WsCloseMsg {
  readonly t: "ws-close";
  readonly id: number;
  readonly code: number;
  readonly reason: string;
}

export type CtrlMsg =
  | ReqMsg
  | ResMsg
  | EndMsg
  | AbortMsg
  | PingMsg
  | PongMsg
  | HelloMsg
  | WsOpenMsg
  | WsOkMsg
  | WsCloseMsg;

const MAX_UINT32 = 0xffffffff;
const METHOD_RE = /^[A-Z]+$/;

/** Subprotocol name: an RFC 6455 token (may not be empty). Same as the Go side. */
const WS_PROTO_RE = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

/**
 * WebSocket close code bounds — the SAME as the Go side.
 *
 * 1005/1006 are inside the range: the WebSocket protocol forbids them on
 * the wire, but as a bridge we have to report an abnormal close to the
 * visitor.
 */
export const WS_CLOSE_MIN = 1000;
export const WS_CLOSE_MAX = 4999;
export const WS_CLOSE_REASON_MAX = 123;

type Raw = Record<string, unknown>;

function has(o: Raw, k: string): boolean {
  return Object.hasOwn(o, k) && o[k] !== undefined;
}

function reqField(o: Raw, k: string, t: string): unknown {
  if (!has(o, k)) {
    throw new CtrlError("missing-field", `${t}.${k} is missing`);
  }
  return o[k];
}

function uint32Field(o: Raw, k: string, t: string): number {
  const v = reqField(o, k, t);
  if (typeof v !== "number" || !Number.isInteger(v) || v < 0 || v > MAX_UINT32) {
    throw new CtrlError("bad-field", `${t}.${k} must be a uint32, got: ${JSON.stringify(v)}`);
  }
  return v;
}

function stringField(o: Raw, k: string, t: string): string {
  const v = reqField(o, k, t);
  if (typeof v !== "string") {
    throw new CtrlError("bad-field", `${t}.${k} must be a string, got: ${JSON.stringify(v)}`);
  }
  return v;
}

function nonEmptyStringField(o: Raw, k: string, t: string): string {
  const v = stringField(o, k, t);
  if (v === "") {
    throw new CtrlError("bad-field", `${t}.${k} must not be empty`);
  }
  return v;
}

function boolField(o: Raw, k: string, t: string): boolean {
  const v = reqField(o, k, t);
  if (typeof v !== "boolean") {
    throw new CtrlError("bad-field", `${t}.${k} must be a boolean, got: ${JSON.stringify(v)}`);
  }
  return v;
}

function headersField(o: Raw, k: string, t: string): Record<string, string> {
  const v = reqField(o, k, t);
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    throw new CtrlError("bad-field", `${t}.${k} must be an object`);
  }
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(v as Raw)) {
    if (typeof value !== "string") {
      throw new CtrlError("bad-field", `${t}.${k}["${name}"] must be a string`);
    }
    // Header names are lowercase in the canonical form
    // (protocol/README.md).
    out[name.toLowerCase()] = value;
  }
  return out;
}

/**
 * First-layer validation of the request path.
 *
 * The deciding layer is `internal/target.Resolve` in the agent (BRIEF
 * principle 1); here we only reject the obviously invalid.
 */
function validatePath(p: string, t: string): string {
  if (p === "") {
    throw new CtrlError("bad-field", `${t}.path must not be empty`);
  }
  if (!p.startsWith("/")) {
    throw new CtrlError("bad-field", `${t}.path must start with "/", got: ${JSON.stringify(p)}`);
  }
  if (p.startsWith("//")) {
    throw new CtrlError("bad-field", `${t}.path must not be protocol-relative`);
  }
  return p;
}

/**
 * Parses and validates text arriving on the `ctrl` channel.
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
    throw new CtrlError("bad-json", "the top level must be a JSON object");
  }
  const o = raw as Raw;

  if (!has(o, "t")) {
    throw new CtrlError("missing-field", "t is missing");
  }
  const t = o.t;
  if (typeof t !== "string") {
    throw new CtrlError("bad-field", `t must be a string, got: ${JSON.stringify(t)}`);
  }

  switch (t) {
    case "req": {
      const method = nonEmptyStringField(o, "method", t);
      if (!METHOD_RE.test(method)) {
        throw new CtrlError("bad-field", `req.method must be an uppercase HTTP token: ${method}`);
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
        throw new CtrlError("bad-field", `res.status must be 100-599, got: ${String(status)}`);
      }
      return { t: "res", id, status, headers: headersField(o, "headers", t) };
    }
    case "end": {
      const id = uint32Field(o, "id", t);
      const frames = uint32Field(o, "frames", t);
      const len = reqField(o, "len", t);
      if (typeof len !== "number" || !Number.isInteger(len) || len < 0) {
        throw new CtrlError("bad-field", `end.len must be a non-negative integer: ${String(len)}`);
      }
      // frames and len must agree: if one is 0 so is the other.
      if ((frames === 0) !== (len === 0)) {
        throw new CtrlError(
          "bad-field",
          `end.frames=${frames} is inconsistent with end.len=${len}`,
        );
      }
      return { t: "end", id, frames, len };
    }
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
        throw new CtrlError("bad-field", `hello.v must be ${PROTOCOL_VERSION}, got: ${v}`);
      }
      const targetsRaw = reqField(o, "targets", t);
      if (!Array.isArray(targetsRaw) || targetsRaw.length === 0) {
        throw new CtrlError("bad-field", "hello.targets must be an array with at least one item");
      }
      const targets: string[] = [];
      for (const item of targetsRaw) {
        if (typeof item !== "string" || item === "") {
          throw new CtrlError("bad-field", "hello.targets items must be non-empty strings");
        }
        targets.push(item);
      }
      const auth = stringField(o, "auth", t);
      if (auth !== "none" && auth !== "required") {
        throw new CtrlError("bad-field", `hello.auth must be "none" or "required": ${auth}`);
      }
      return { t: "hello", v, targets, auth };
    }
    case "ws-open": {
      const id = uint32Field(o, "id", t);
      const path = validatePath(stringField(o, "path", t), t);
      const protosRaw = reqField(o, "protos", t);
      if (!Array.isArray(protosRaw)) {
        throw new CtrlError("bad-field", "ws-open.protos must be an array of strings");
      }
      const protos: string[] = [];
      for (const item of protosRaw) {
        if (typeof item !== "string" || !WS_PROTO_RE.test(item)) {
          throw new CtrlError(
            "bad-field",
            `ws-open.protos items must be tokens: ${JSON.stringify(item)}`,
          );
        }
        protos.push(item);
      }
      return { t: "ws-open", id, path, protos };
    }
    case "ws-ok": {
      const id = uint32Field(o, "id", t);
      // An empty string is valid: the server may have chosen no
      // subprotocol.
      const proto = stringField(o, "proto", t);
      if (proto !== "" && !WS_PROTO_RE.test(proto)) {
        throw new CtrlError("bad-field", `ws-ok.proto must be a token: ${JSON.stringify(proto)}`);
      }
      return { t: "ws-ok", id, proto };
    }
    case "ws-close": {
      const id = uint32Field(o, "id", t);
      const code = reqField(o, "code", t);
      if (typeof code !== "number" || !Number.isInteger(code)) {
        throw new CtrlError("bad-field", `ws-close.code must be an integer, got: ${String(code)}`);
      }
      if (code < WS_CLOSE_MIN || code > WS_CLOSE_MAX) {
        throw new CtrlError(
          "bad-field",
          `ws-close.code must be ${WS_CLOSE_MIN}-${WS_CLOSE_MAX}, got: ${code}`,
        );
      }
      // An empty string is valid: a WebSocket close reason is optional.
      const reason = stringField(o, "reason", t);
      const bytes = new TextEncoder().encode(reason).length;
      if (bytes > WS_CLOSE_REASON_MAX) {
        throw new CtrlError(
          "bad-field",
          `ws-close.reason may be at most ${WS_CLOSE_REASON_MAX} bytes, got: ${bytes}`,
        );
      }
      return { t: "ws-close", id, code, reason };
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
 * Serializes a message in canonical form: no whitespace, key order fixed
 * per type, `headers` lowercase and sorted (protocol/README.md).
 *
 * Produces **byte-for-byte the same** output as `Canonical` on the Go
 * side; the shared test vectors check that.
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
      return `{"t":"end","id":${msg.id},"frames":${msg.frames},"len":${msg.len}}`;
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
    case "ws-open":
      return `{"t":"ws-open","id":${msg.id},"path":${JSON.stringify(msg.path)},"protos":[${msg.protos
        .map((x) => JSON.stringify(x))
        .join(",")}]}`;
    case "ws-ok":
      return `{"t":"ws-ok","id":${msg.id},"proto":${JSON.stringify(msg.proto)}}`;
    case "ws-close":
      return `{"t":"ws-close","id":${msg.id},"code":${msg.code},"reason":${JSON.stringify(
        msg.reason,
      )}}`;
  }
}
