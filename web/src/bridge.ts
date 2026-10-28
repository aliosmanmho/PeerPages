/**
 * Message format between the Service Worker and the tunnel page.
 *
 * There is NO WebRTC inside a Service Worker (BRIEF section 2): the page
 * holds the connection. The SW forwards every `fetch` it catches to the
 * page over a `MessageChannel`, the page sends the response back chunk by
 * chunk, and the SW serves it as a `ReadableStream`.
 *
 * This file is imported by both the app (DOM) and the SW (WebWorker), so
 * it uses no environment-specific types.
 */

/** The SW asking the page for a request. The reply comes over `port`. */
export interface BridgeRequest {
  readonly type: "bridge-req";
  readonly method: string;
  /** Path for the local server: scope prefix stripped, starts with "/". */
  readonly path: string;
  readonly headers: Record<string, string>;
  /** Request body, or null. In phase 1 the SW buffers the body. */
  readonly body: ArrayBuffer | null;
}

/** The page answering "do you have the tunnel". */
export interface BridgeProbe {
  readonly type: "bridge-probe";
}

export interface BridgeProbeReply {
  readonly type: "bridge-probe-reply";
  readonly hasTunnel: boolean;
}

/** The page registering itself as the tunnel owner. */
export interface BridgeTunnelReady {
  readonly type: "bridge-tunnel-ready";
}

/** The page reporting that the tunnel closed. */
export interface BridgeTunnelGone {
  readonly type: "bridge-tunnel-gone";
}

/**
 * The page saying "the tunnel dropped but I am reconnecting" (phase 4).
 *
 * After this news the SW waits a short while for the tunnel to come back
 * instead of 503-ing immediately: after sleep/wake the site the visitor is
 * browsing looks slow rather than broken.
 */
export interface BridgeTunnelReconnecting {
  readonly type: "bridge-tunnel-reconnecting";
}

/**
 * The shim asking to open a WebSocket (phase 6): visitor page to SW.
 *
 * The shim runs in the visitor tab while the tunnel lives in ANOTHER tab.
 * The SW only steps in for the HANDSHAKE: it finds the tunnel owner and
 * passes the shim's `MessagePort` along untouched. After that it is page
 * to page, because the SW can be killed while idle and a long-lived
 * socket cannot depend on it.
 */
export interface BridgeWsOpen {
  readonly type: "bridge-ws-open";
  /** Path for the local server: scope prefix stripped, starts with "/". */
  readonly path: string;
  readonly protos: readonly string[];
}

/** The SW forwarding that same request to the tunnel-owner page. */
export interface BridgeWsOpenForward {
  readonly type: "bridge-ws-open-forward";
  readonly path: string;
  readonly protos: readonly string[];
}

/** Messages on the socket port (shim <-> tunnel-owner page). */
export interface WsPortOpened {
  readonly type: "ws-opened";
  readonly protocol: string;
}

export interface WsPortMessage {
  readonly type: "ws-message";
  readonly binary: boolean;
  /** Mesaj govdesi; transfer edilir, kopyalanmaz. */
  readonly data: ArrayBuffer;
}

export interface WsPortClose {
  readonly type: "ws-close";
  readonly code: number;
  readonly reason: string;
}

/** A send request coming from the shim. */
export interface WsPortSend {
  readonly type: "ws-send";
  readonly binary: boolean;
  readonly data: ArrayBuffer;
}

/** Messages going from the page to the shim. */
export type WsPortToShim = WsPortOpened | WsPortMessage | WsPortClose;
/** Messages going from the shim to the page. */
export type WsPortToPage = WsPortSend | WsPortClose;

export type BridgeToPage = BridgeRequest | BridgeProbe | BridgeWsOpenForward;
export type BridgeToWorker =
  | BridgeTunnelReady
  | BridgeTunnelGone
  | BridgeTunnelReconnecting
  | BridgeProbeReply
  | BridgeWsOpen;

/** Response head. */
export interface BridgeResponseHead {
  readonly type: "res";
  readonly status: number;
  readonly headers: Record<string, string>;
}

/** A chunk of the response body. `data` is transferred, not copied. */
export interface BridgeResponseChunk {
  readonly type: "chunk";
  readonly data: ArrayBuffer;
}

/** Body finished. */
export interface BridgeResponseEnd {
  readonly type: "end";
}

/** The request failed. */
export interface BridgeResponseError {
  readonly type: "error";
  readonly message: string;
  /**
   * The error means "there is no tunnel right now": the request never
   * reached the target (phase 4).
   *
   * Seeing this — and only for an idempotent request — the SW waits
   * briefly for the reconnect and retries ONCE. The distinction matters:
   * an error from the target must not be retried.
   */
  readonly retryable?: boolean;
}

export type BridgeResponse =
  | BridgeResponseHead
  | BridgeResponseChunk
  | BridgeResponseEnd
  | BridgeResponseError;

/**
 * Largest request body the SW will buffer.
 *
 * A phase 1 limitation: request bodies are not streamed, the SW buffers
 * them whole. The agent's --max-body default is 50MB; we keep a lower
 * ceiling here because the body sits in browser memory.
 */
export const MAX_BUFFERED_REQUEST_BODY = 16 * 1024 * 1024;

/** How long to wait for the page to answer a probe. */
export const PROBE_TIMEOUT_MS = 1000;

/**
 * Longest a request is held during a reconnect gap (phase 4).
 *
 * An upper bound is REQUIRED: without it, requests hang forever once the
 * agent is really gone and the tab looks frozen.
 */
export const RECONNECT_WAIT_MS = 10_000;

/**
 * Navigation requests wait less: staring at a blank tab is worse than
 * landing back on the connect screen with its status and countdown.
 */
export const RECONNECT_WAIT_NAVIGATE_MS = 3_000;

/**
 * How long the "I am reconnecting" news stays valid.
 *
 * The page repeats the news on every attempt; once this window expires
 * the SW stops holding requests, because the page may be dead (after a
 * crash, or the OS discarding the tab, the "I gave up" news never comes).
 */
export const RECONNECT_WINDOW_MS = 60_000;

/**
 * Path the shim is served from (under the scope root).
 *
 * It does NOT have to match the agent: the agent only adds the
 * `x-peerpages-ws-shim` header, the SW decides the path. But
 * `isAppShellPath` has to know this name, or the shim's own request goes
 * into the tunnel.
 */
export const WS_SHIM_FILE = "ws-shim.js";

/**
 * The marker the agent adds to HTML responses (phase 6).
 *
 * Must be IDENTICAL to `protocol.HeaderWSShim` on the Go side
 * (protocol/README.md).
 */
export const WS_SHIM_HEADER = "x-peerpages-ws-shim";
