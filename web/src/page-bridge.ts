/**
 * The page end of the bridge to the Service Worker.
 *
 * The SW forwards every request it catches here over a `MessageChannel`;
 * this file turns it into a tunnel request and sends the response back
 * chunk by chunk. The SW serves it as a `ReadableStream`, so the full body
 * is never awaited.
 */

import type {
  BridgeRequest,
  BridgeResponse,
  BridgeToPage,
  BridgeWsOpenForward,
  WsPortToPage,
  WsPortToShim,
} from "./bridge";
import type { Tunnel, TunnelSocket } from "./tunnel";

export interface BridgeHooks {
  /** Called when a request finishes (for the status panel counters). */
  onRequestDone?: (info: { path: string; status: number; bytes: number }) => void;
  onRequestError?: (info: { path: string; message: string }) => void;
  /** Number of open ws sessions changed (phase 6, for the status panel). */
  onSocketChange?: (open: number) => void;
}

/**
 * Starts listening to SW messages.
 *
 * `getTunnel` returns the tunnel that is active at call time, or null: a
 * tunnel can be rebuilt, which is why this takes a function rather than a
 * reference.
 */
export function installBridge(getTunnel: () => Tunnel | null, hooks: BridgeHooks = {}): void {
  if (!("serviceWorker" in navigator)) return;

  navigator.serviceWorker.addEventListener("message", (event: MessageEvent) => {
    const msg = event.data as BridgeToPage | undefined;
    if (!msg || typeof msg !== "object") return;

    const port = event.ports[0];

    switch (msg.type) {
      case "bridge-probe": {
        const tunnel = getTunnel();
        port?.postMessage({
          type: "bridge-probe-reply",
          hasTunnel: tunnel?.isOpen === true,
        });
        port?.close();
        break;
      }
      case "bridge-req": {
        if (!port) return;
        void serveRequest(msg, port, getTunnel(), hooks);
        break;
      }
      case "bridge-ws-open-forward": {
        if (!port) return;
        serveSocket(msg, port, getTunnel(), hooks);
        break;
      }
      default:
        break;
    }
  });
}

/** The page registering itself as the tunnel owner. */
export function announceTunnelReady(): void {
  navigator.serviceWorker?.controller?.postMessage({ type: "bridge-tunnel-ready" });
}

/** Tells the SW the tunnel has closed. */
export function announceTunnelGone(): void {
  navigator.serviceWorker?.controller?.postMessage({ type: "bridge-tunnel-gone" });
}

/**
 * Tells the SW the tunnel dropped but is being rebuilt (phase 4).
 *
 * Once the SW hears this it holds requests for a short while, so the site
 * the visitor is browsing waits for the reconnect instead of getting a 503
 * the moment the tunnel drops.
 */
export function announceTunnelReconnecting(): void {
  navigator.serviceWorker?.controller?.postMessage({ type: "bridge-tunnel-reconnecting" });
}

/**
 * Wires the shim's ws session to the tunnel (phase 6).
 *
 * The port is pumped both ways: `ws-send` from the shim goes to the
 * tunnel, messages from the tunnel go to the shim. From this point on the
 * SW is NOT involved.
 */
function serveSocket(
  msg: BridgeWsOpenForward,
  port: MessagePort,
  tunnel: Tunnel | null,
  hooks: BridgeHooks,
): void {
  const fail = (code: number, reason: string) => {
    port.postMessage({ type: "ws-close", code, reason } satisfies WsPortToShim);
    port.close();
  };

  if (!tunnel?.isOpen) {
    fail(1013, "tunnel is not open");
    return;
  }

  let socket: TunnelSocket;
  try {
    socket = tunnel.openSocket(msg.path, msg.protos, {
      onOpen: (proto) => {
        port.postMessage({ type: "ws-opened", protocol: proto } satisfies WsPortToShim);
        hooks.onSocketChange?.(tunnel.socketCount);
      },
      onMessage: (binary, data) => {
        // Transfer instead of copying (same reasoning as request bodies):
        // the slice's buffer may be shared, so we take its own buffer out.
        const buf = data.slice().buffer;
        port.postMessage({ type: "ws-message", binary, data: buf } satisfies WsPortToShim, [buf]);
      },
      onClose: (code, reason) => {
        port.postMessage({ type: "ws-close", code, reason } satisfies WsPortToShim);
        port.close();
        hooks.onSocketChange?.(tunnel.socketCount);
      },
    });
  } catch (err) {
    fail(1011, err instanceof Error ? err.message : String(err));
    return;
  }

  hooks.onSocketChange?.(tunnel.socketCount);

  port.onmessage = (event: MessageEvent) => {
    const m = event.data as WsPortToPage | undefined;
    if (!m || typeof m !== "object") return;
    switch (m.type) {
      case "ws-send":
        // A send failure already closes the session; we swallow it here
        // so no unhandled promise rejection is produced.
        void socket.send(m.binary, new Uint8Array(m.data)).catch(() => {});
        break;
      case "ws-close":
        socket.close(m.code, m.reason);
        break;
      default:
        break;
    }
  };
}

async function serveRequest(
  msg: BridgeRequest,
  port: MessagePort,
  tunnel: Tunnel | null,
  hooks: BridgeHooks,
): Promise<void> {
  const send = (payload: BridgeResponse, transfer?: Transferable[]) => {
    if (transfer && transfer.length > 0) {
      port.postMessage(payload, transfer);
      return;
    }
    port.postMessage(payload);
  };

  if (!tunnel?.isOpen) {
    // The request never reached the target: the SW can wait for the
    // reconnect and try again.
    send({ type: "error", message: "tunnel is not open", retryable: true });
    port.close();
    hooks.onRequestError?.({ path: msg.path, message: "tunnel is not open" });
    return;
  }

  let bytes = 0;
  let headSent = false;
  try {
    const res = await tunnel.request({
      method: msg.method,
      path: msg.path,
      headers: msg.headers,
      body: msg.body ? new Uint8Array(msg.body) : null,
    });

    headSent = true;
    send({ type: "res", status: res.status, headers: { ...res.headers } });

    const reader = res.body.getReader();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value || value.byteLength === 0) continue;
      bytes += value.byteLength;

      // Transfer instead of copying: it shows up on a 5 MB download.
      // Note: value's buffer may be shared, so we take the slice's own
      // buffer out.
      const copy = value.slice().buffer;
      send({ type: "chunk", data: copy }, [copy]);
    }

    send({ type: "end" });
    port.close();
    hooks.onRequestDone?.({ path: msg.path, status: res.status, bytes });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    // If the response head never went out AND the tunnel is now closed,
    // the request died because the tunnel dropped: the SW can wait for the
    // reconnect and retry. Errors from the target (500 and friends) sent a
    // head, so they never take this path.
    const retryable = !headSent && !tunnel.isOpen;
    send({ type: "error", message, retryable });
    port.close();
    hooks.onRequestError?.({ path: msg.path, message });
  }
}
