/**
 * The `window.WebSocket` shim, injected into pages served through the
 * tunnel (phase 6).
 *
 * Why it is needed: a Service Worker only catches `fetch` events, the
 * WebSocket handshake is NOT in its reach. Without the shim, a
 * `new WebSocket(...)` call on a tunneled page goes to the real network
 * and fails — which means Vite HMR, Socket.IO and every dev server with a
 * live preview only half works.
 *
 * How it works: the shim runs in the visitor's tab, the tunnel lives in
 * ANOTHER tab. A `MessageChannel` is set up and its port is sent to the
 * SW, which TRANSFERS it to the tunnel-owner page; from then on it is
 * page <-> page (the SW may be killed while idle).
 *
 * This file is built as a CLASSIC script (not a module) and must run
 * BEFORE the page's own scripts.
 */

import { WS_SHIM_FILE, type WsPortToPage, type WsPortToShim } from "./bridge";

type Listener = ((ev: Event) => void) | null;

const CONNECTING = 0 as const;
const OPEN = 1 as const;
const CLOSING = 2 as const;
const CLOSED = 3 as const;

/** A type-safe WebSocket.readyState. */
type ReadyState = typeof CONNECTING | typeof OPEN | typeof CLOSING | typeof CLOSED;

/** Marks the shim as already installed (it can be injected twice). */
const INSTALLED = "__peerpagesWsShim";

/**
 * The directory the shim is served from = the app shell's scope root.
 *
 * Computed from `document.currentScript`: no value is injected, so
 * neither the agent nor the SW has to know the scope prefix.
 */
export function scopeFromSrc(src: string | undefined, base: string): string {
  if (!src) return "/";
  try {
    const u = new URL(src, base);
    if (!u.pathname.endsWith(`/${WS_SHIM_FILE}`)) return "/";
    return u.pathname.slice(0, u.pathname.length - WS_SHIM_FILE.length);
  } catch {
    return "/";
  }
}

/**
 * Should this URL go through the tunnel?
 *
 * The rule: only the SAME HOSTNAME (the port may differ). Every other
 * address goes to the real `WebSocket` — we do not touch anything outside
 * our own scope (the shim's counterpart of BRIEF principle 5).
 *
 * Why the port does not matter: Vite HMR can put its own port in the
 * address (`wss://host:5173/`), but that port is meaningless in the
 * visitor's browser; the target is decided by the agent's allowlist.
 */
export function isTunneled(u: URL, hostname: string): boolean {
  return u.hostname === hostname;
}

/**
 * Builds the path that goes to the local server.
 *
 * A scope prefix, if any, is stripped (`/PeerPages/ws` -> `/ws`).
 * Otherwise the path is used as is: a root-absolute ws address
 * (`wss://host/`) belongs to the tunneled page's own site — the shim is
 * only injected into those pages. Vite HMR is exactly this case
 * (`__HMR_BASE__` becomes "/").
 */
export function localPath(u: URL, scope: string): string {
  const full = u.pathname + u.search;
  if (scope !== "/" && u.pathname.startsWith(scope)) {
    return `/${full.slice(scope.length)}`;
  }
  return full === "" ? "/" : full;
}

function install(): void {
  const w = window as unknown as Record<string, unknown>;
  if (w[INSTALLED]) return;

  const Native = window.WebSocket;
  if (!Native || !("serviceWorker" in navigator)) return;

  const scope = scopeFromSrc(
    (document.currentScript as HTMLScriptElement | null)?.src,
    location.href,
  );

  class PeerPagesWebSocket extends EventTarget implements WebSocket {
    static readonly CONNECTING = CONNECTING;
    static readonly OPEN = OPEN;
    static readonly CLOSING = CLOSING;
    static readonly CLOSED = CLOSED;

    readonly CONNECTING = CONNECTING;
    readonly OPEN = OPEN;
    readonly CLOSING = CLOSING;
    readonly CLOSED = CLOSED;

    readonly url: string;
    binaryType: BinaryType = "blob";
    readyState: ReadyState = CONNECTING;
    protocol = "";
    readonly extensions = "";

    onopen: Listener = null;
    onmessage: Listener = null;
    onerror: Listener = null;
    onclose: Listener = null;

    private port: MessagePort | null = null;
    /** Send chain: reading a Blob is async, keep the order intact. */
    private chain: Promise<void> = Promise.resolve();

    constructor(url: string | URL, protocols?: string | string[]) {
      super();
      const abs = new URL(String(url), location.href);
      this.url = abs.href;

      const protos = protocols === undefined ? [] : [protocols].flat();
      const channel = new MessageChannel();
      this.port = channel.port1;
      channel.port1.onmessage = (ev: MessageEvent) => {
        this.handle(ev.data as WsPortToShim | undefined);
      };

      const controller = navigator.serviceWorker.controller;
      if (!controller) {
        // No SW means no tunnel: close right away and let the page's own
        // retry take over.
        this.fail(1011, "peerpages: no service worker");
        return;
      }
      controller.postMessage({ type: "bridge-ws-open", path: localPath(abs, scope), protos }, [
        channel.port2,
      ]);
    }

    /** `bufferedAmount` is always 0: the real buffer is in the tunnel. */
    get bufferedAmount(): number {
      return 0;
    }

    send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
      if (this.readyState === CONNECTING) {
        // A real WebSocket behaves the same way.
        throw new DOMException("WebSocket is not open yet", "InvalidStateError");
      }
      if (this.readyState !== OPEN || !this.port) return;

      const port = this.port;
      this.chain = this.chain.then(async () => {
        const { binary, buffer } = await toBuffer(data);
        if (this.readyState !== OPEN) return;
        port.postMessage({ type: "ws-send", binary, data: buffer } satisfies WsPortToPage, [
          buffer,
        ]);
      });
    }

    close(code = 1000, reason = ""): void {
      if (this.readyState === CLOSING || this.readyState === CLOSED) return;
      this.readyState = CLOSING;
      this.port?.postMessage({ type: "ws-close", code, reason } satisfies WsPortToPage);
      // We do not wait for the agent to confirm the close: a real
      // WebSocket also goes CLOSING after `close()` and CLOSED on
      // confirmation.
    }

    private handle(msg: WsPortToShim | undefined): void {
      if (!msg || typeof msg !== "object") return;
      switch (msg.type) {
        case "ws-opened":
          if (this.readyState !== CONNECTING) return;
          this.readyState = OPEN;
          this.protocol = msg.protocol;
          this.fire(new Event("open"), "onopen");
          break;
        case "ws-message": {
          if (this.readyState !== OPEN) return;
          const data = msg.binary ? this.decodeBinary(msg.data) : textOf(msg.data);
          this.fire(new MessageEvent("message", { data, origin: originOf(this.url) }), "onmessage");
          break;
        }
        case "ws-close":
          this.finish(msg.code, msg.reason);
          break;
        default:
          break;
      }
    }

    private decodeBinary(buf: ArrayBuffer): ArrayBuffer | Blob {
      return this.binaryType === "arraybuffer" ? buf : new Blob([buf]);
    }

    /** The connection died before it was ever established. */
    private fail(code: number, reason: string): void {
      // Order matters: a real WebSocket also sends error before close,
      // and both arrive after a microtask.
      queueMicrotask(() => {
        this.fire(new Event("error"), "onerror");
        this.finish(code, reason);
      });
    }

    private finish(code: number, reason: string): void {
      if (this.readyState === CLOSED) return;
      const wasClean = this.readyState !== CONNECTING && code === 1000;
      this.readyState = CLOSED;
      this.port?.close();
      this.port = null;
      this.fire(new CloseEvent("close", { code, reason, wasClean }), "onclose");
    }

    private fire(ev: Event, handler: "onopen" | "onmessage" | "onerror" | "onclose"): void {
      const fn = this[handler];
      if (typeof fn === "function") {
        try {
          fn.call(this, ev);
        } catch (err) {
          console.error("[peerpages] a ws listener threw:", err);
        }
      }
      this.dispatchEvent(ev);
    }
  }

  // Addresses that do not enter the tunnel go to the REAL WebSocket.
  const Wrapper = function WebSocket(
    this: unknown,
    url: string | URL,
    protocols?: string | string[],
  ) {
    let abs: URL;
    try {
      abs = new URL(String(url), location.href);
    } catch {
      return new Native(url as string, protocols);
    }
    if (!isTunneled(abs, location.hostname)) {
      return new Native(url as string, protocols);
    }
    return new PeerPagesWebSocket(url, protocols);
  } as unknown as typeof WebSocket;

  Wrapper.prototype = PeerPagesWebSocket.prototype;
  Object.defineProperties(Wrapper, {
    CONNECTING: { value: CONNECTING },
    OPEN: { value: OPEN },
    CLOSING: { value: CLOSING },
    CLOSED: { value: CLOSED },
  });

  window.WebSocket = Wrapper;
  w[INSTALLED] = true;
}

/** Turns outgoing data into a (binary, ArrayBuffer) pair. */
async function toBuffer(
  data: string | ArrayBufferLike | Blob | ArrayBufferView,
): Promise<{ binary: boolean; buffer: ArrayBuffer }> {
  if (typeof data === "string") {
    return { binary: false, buffer: new TextEncoder().encode(data).buffer as ArrayBuffer };
  }
  if (data instanceof Blob) {
    return { binary: true, buffer: await data.arrayBuffer() };
  }
  if (ArrayBuffer.isView(data)) {
    // Take the view's OWN buffer out: a shared buffer cannot go into the
    // transfer list.
    return {
      binary: true,
      buffer: data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength) as ArrayBuffer,
    };
  }
  return { binary: true, buffer: (data as ArrayBuffer).slice(0) };
}

function textOf(buf: ArrayBuffer): string {
  return new TextDecoder().decode(buf);
}

function originOf(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return location.origin;
  }
}

// So tests can import this file in a node environment (the pure helpers
// are exported); in a browser it always installs.
if (typeof window !== "undefined" && typeof document !== "undefined") {
  install();
}
