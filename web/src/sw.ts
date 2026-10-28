/// <reference lib="webworker" />

/**
 * PeerPages Service Worker — hand-written, built by hand
 * (a BRIEF decision: no vite-plugin-pwa).
 *
 * Its job: catch EVERY `fetch` inside its own scope, hand it to the
 * tunnel page, and stream the response back. That is what makes relative
 * links, CSS, JS, images and fetch/XHR work transparently (BRIEF §2).
 *
 * Limits, on purpose:
 *  - no WebRTC inside a SW, so the page holds the connection and the SW
 *    is only a bridge;
 *  - with the tunnel page closed, navigations are redirected to the
 *    connect screen and subresources get a 503.
 */

import { isAppShellPath } from "./base";
import {
  type BridgeResponse,
  type BridgeResponseHead,
  type BridgeToWorker,
  MAX_BUFFERED_REQUEST_BODY,
  PROBE_TIMEOUT_MS,
  RECONNECT_WAIT_MS,
  RECONNECT_WAIT_NAVIGATE_MS,
  RECONNECT_WINDOW_MS,
  WS_SHIM_FILE,
  WS_SHIM_HEADER,
} from "./bridge";
import { injectScriptStream } from "./inject";

declare const self: ServiceWorkerGlobalScope;

/**
 * The SW scope — decided at RUNTIME, not at build time.
 *
 * We do not use `import.meta.env.BASE_URL` because the SW goes through a
 * separate vite build and the two builds' `base` values can drift apart
 * (they did once: the SW trimmed the path prefix wrongly and asked the
 * local server for /PeerPages/index.html, a 404). `registration.scope` is
 * always right: it is the scope the page registered the SW with.
 */
const SCOPE_PATH = new URL(self.registration.scope).pathname;

/** Client id of the page holding the tunnel; refound if the SW restarts. */
let tunnelClientId: string | null = null;

/** When we last confirmed that client really has the tunnel. */
let tunnelSeenAt = 0;

/**
 * How long a cached client id is trusted before it is probed again.
 *
 * Why it is needed: `clients.get(id)` returns a client as long as the tab
 * is ALIVE — even after it lost the tunnel. The page's pagehide message
 * ("tunnel gone") is BEST EFFORT: it never arrives if the tab crashes, is
 * killed, or navigates away. After this window we probe again so requests
 * are not routed into a dead tunnel.
 *
 * Detection is LAZY: no request, no check. The real time-bounded
 * protection is the idle watchdog on the agent (agent/internal/tunnel).
 * A periodic timer in a SW is unreliable: an idle worker gets killed.
 */
const TUNNEL_TRUST_MS = 5000;

/**
 * The page said "I am reconnecting" and holding requests is worth it
 * until this moment (phase 4). Zero means no holding.
 */
let reconnectingUntil = 0;

/** Requests waiting for the tunnel to come back. */
let tunnelWaiters: Array<() => void> = [];

/** Tunnel is back: release every waiting request together. */
function releaseTunnelWaiters(): void {
  const waiters = tunnelWaiters;
  tunnelWaiters = [];
  for (const w of waiters) w();
}

/**
 * Waits at most `ms` for the tunnel to come back.
 *
 * A fetch event keeps the worker alive while its response is pending, so
 * a timer is reliable here — the unreliability of a PERIODIC timer in a
 * SW is a different matter (an idle worker gets killed).
 */
function waitForTunnel(ms: number): Promise<void> {
  return new Promise<void>((resolve) => {
    const done = () => {
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(() => {
      tunnelWaiters = tunnelWaiters.filter((w) => w !== done);
      resolve();
    }, ms);
    tunnelWaiters.push(done);
  });
}

self.addEventListener("install", () => {
  // Take over immediately so the user does not have to close and reopen
  self.skipWaiting();
});

self.addEventListener("activate", (event: ExtendableEvent) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("message", (event: ExtendableMessageEvent) => {
  const msg = event.data as BridgeToWorker | undefined;
  if (!msg || typeof msg !== "object") return;

  switch (msg.type) {
    case "bridge-tunnel-ready": {
      const source = event.source;
      if (source && "id" in source) {
        tunnelClientId = source.id;
        tunnelSeenAt = Date.now();
      }
      reconnectingUntil = 0;
      releaseTunnelWaiters();
      break;
    }
    case "bridge-tunnel-gone":
      tunnelClientId = null;
      tunnelSeenAt = 0;
      reconnectingUntil = 0;
      releaseTunnelWaiters();
      break;
    case "bridge-tunnel-reconnecting":
      tunnelClientId = null;
      tunnelSeenAt = 0;
      reconnectingUntil = Date.now() + RECONNECT_WINDOW_MS;
      break;
    case "bridge-ws-open": {
      // The shim's WebSocket request (phase 6): find the tunnel owner and
      // PASS the port along. After that it is page to page; the SW can
      // be killed while idle and a long-lived socket cannot rely on it.
      const port = event.ports[0];
      if (!port) break;
      event.waitUntil(forwardWsOpen(msg.path, msg.protos, port));
      break;
    }
    default:
      break;
  }
});

/**
 * Forwards the shim's ws request to the tunnel-owner page.
 *
 * With no tunnel it closes the port with 1013: the shim sees that as
 * `onclose` and the page runs its own retry logic (both Vite and
 * Socket.IO do).
 */
async function forwardWsOpen(
  path: string,
  protos: readonly string[],
  port: MessagePort,
): Promise<void> {
  const client = await findTunnelClient();
  if (!client) {
    port.postMessage({ type: "ws-close", code: 1013, reason: "no tunnel" });
    port.close();
    return;
  }
  try {
    client.postMessage({ type: "bridge-ws-open-forward", path, protos }, [port]);
  } catch {
    port.postMessage({ type: "ws-close", code: 1011, reason: "bridge failed" });
    port.close();
  }
}

self.addEventListener("fetch", (event: FetchEvent) => {
  const request = event.request;

  // Only requests inside our own scope (BRIEF security principle 5).
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (!url.pathname.startsWith(SCOPE_PATH)) return;

  // The app shell never sends its own files through the tunnel: we would
  // need a tunnel to open the connect screen (chicken and egg).
  if (isAppShellPath(url.pathname, SCOPE_PATH)) return;

  event.respondWith(handle(request, url));
});

async function handle(request: Request, url: URL): Promise<Response> {
  let client = await findTunnelClient();

  // If the page is reconnecting we do not reject the request outright:
  // after sleep/wake the site looks slow rather than broken.
  if (!client && Date.now() < reconnectingUntil) {
    const waitMs = request.mode === "navigate" ? RECONNECT_WAIT_NAVIGATE_MS : RECONNECT_WAIT_MS;
    await waitForTunnel(waitMs);
    client = await findTunnelClient();
  }

  if (!client) {
    if (request.mode === "navigate") {
      // Back to the connect screen; connect/ opens without the tunnel.
      return Response.redirect(
        new URL(`${SCOPE_PATH}connect/`, self.location.origin).toString(),
        302,
      );
    }
    return new Response("peerpages: tunnel is not open\n", {
      status: 503,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "cache-control": "no-store",
      },
    });
  }

  let body: ArrayBuffer | null = null;
  if (request.method !== "GET" && request.method !== "HEAD") {
    try {
      body = await readLimitedBody(request);
    } catch (err) {
      return new Response(`peerpages: request body unreadable (${String(err)})\n`, {
        status: 413,
        headers: { "content-type": "text/plain; charset=utf-8" },
      });
    }
  }

  let response = await forward(client, request, url, body);

  // If the tunnel dropped during this very request the page returns a
  // "retryable" error, meaning the request never reached the target. We
  // wait briefly for the reconnect and try ONCE more — only for
  // idempotent requests, since we cannot know whether a POST arrived.
  if (response === RETRY_TUNNEL) {
    const idempotent = request.method === "GET" || request.method === "HEAD";
    if (idempotent) {
      await waitForTunnel(
        request.mode === "navigate" ? RECONNECT_WAIT_NAVIGATE_MS : RECONNECT_WAIT_MS,
      );
      const retryClient = await findTunnelClient();
      if (retryClient) {
        response = await forward(retryClient, request, url, body);
      }
    }
  }

  if (response === RETRY_TUNNEL) {
    return new Response("peerpages: tunnel is not open\n", {
      status: 502,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "cache-control": "no-store",
      },
    });
  }
  return response;
}

/**
 * The answer from forward() meaning "there was no tunnel, nothing sent".
 *
 * A separate value rather than a Response: the caller decides whether it
 * may retry.
 */
const RETRY_TUNNEL = Symbol("retry-tunnel");

/** Buffers the request body up to a bounded size. */
async function readLimitedBody(request: Request): Promise<ArrayBuffer | null> {
  const buf = await request.clone().arrayBuffer();
  if (buf.byteLength === 0) return null;
  if (buf.byteLength > MAX_BUFFERED_REQUEST_BODY) {
    throw new Error(`body too large: ${buf.byteLength} bytes`);
  }
  return buf;
}

/**
 * Forwards the request to the tunnel page and streams the response back.
 *
 * The full body is never awaited: a Response is built as soon as `res`
 * arrives and chunks are written to the stream as they come (BRIEF phase 1).
 */
function forward(
  client: Client,
  request: Request,
  url: URL,
  body: ArrayBuffer | null,
): Promise<Response | typeof RETRY_TUNNEL> {
  return new Promise<Response | typeof RETRY_TUNNEL>((resolve, reject) => {
    const channel = new MessageChannel();
    let settled = false;
    let controller: ReadableStreamDefaultController<Uint8Array> | null = null;
    let closed = false;
    const queued: Uint8Array[] = [];

    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        controller = c;
        for (const chunk of queued) c.enqueue(chunk);
        queued.length = 0;
        if (closed) c.close();
      },
      cancel() {
        channel.port1.close();
      },
    });

    const enqueue = (chunk: Uint8Array) => {
      if (controller) {
        controller.enqueue(chunk);
        return;
      }
      queued.push(chunk);
    };

    const finish = () => {
      closed = true;
      if (controller) {
        try {
          controller.close();
        } catch {
          // It may already be closed.
        }
      }
      channel.port1.close();
    };

    const fail = (message: string, retryable = false) => {
      if (!settled) {
        settled = true;
        channel.port1.close();
        if (retryable) {
          resolve(RETRY_TUNNEL);
          return;
        }
        resolve(
          new Response(`peerpages: ${message}\n`, {
            status: 502,
            headers: {
              "content-type": "text/plain; charset=utf-8",
              "cache-control": "no-store",
            },
          }),
        );
        return;
      }
      closed = true;
      if (controller) {
        controller.error(new Error(message));
      }
      channel.port1.close();
    };

    channel.port1.onmessage = (ev: MessageEvent<BridgeResponse>) => {
      const msg = ev.data;
      switch (msg.type) {
        case "res": {
          if (settled) return;
          settled = true;
          resolve(buildResponse(msg, stream));
          break;
        }
        case "chunk":
          enqueue(new Uint8Array(msg.data));
          break;
        case "end":
          finish();
          break;
        case "error":
          fail(msg.message, msg.retryable === true);
          break;
      }
    };

    const headers: Record<string, string> = {};
    request.headers.forEach((value, name) => {
      headers[name.toLowerCase()] = value;
    });

    // The scope prefix is stripped: the local server expects
    // "/index.html" while the visitor asked for "/PeerPages/index.html".
    const path = `/${url.pathname.slice(SCOPE_PATH.length)}${url.search}`;

    try {
      client.postMessage(
        {
          type: "bridge-req",
          method: request.method,
          path,
          headers,
          body,
        },
        body ? [channel.port2, body] : [channel.port2],
      );
    } catch (err) {
      reject(err instanceof Error ? err : new Error(String(err)));
    }
  });
}

/**
 * Builds the response and injects the WebSocket shim when needed (phase 6).
 *
 * Injection happens ONLY when both hold: the agent set the
 * `x-peerpages-ws-shim` marker (i.e. `--inject-ws-shim` was passed) AND
 * the response is HTML. Nothing else is ever touched.
 *
 * The marker does NOT reach the visitor: it is a detail between us and
 * the agent, not something the page should see.
 */
function buildResponse(msg: BridgeResponseHead, stream: ReadableStream<Uint8Array>): Response {
  const headers = new Headers(msg.headers);
  const wantsShim = headers.get(WS_SHIM_HEADER) === "1";
  headers.delete(WS_SHIM_HEADER);

  if (!statusAllowsBody(msg.status)) {
    return new Response(null, { status: msg.status, headers });
  }

  const body =
    wantsShim && isHTMLResponse(headers.get("content-type"))
      ? stream.pipeThrough(injectScriptStream(SCOPE_PATH + WS_SHIM_FILE))
      : stream;

  return new Response(body, { status: msg.status, headers });
}

/** Is this content-type HTML (parameters ignored)? */
function isHTMLResponse(contentType: string | null): boolean {
  if (!contentType) return false;
  const mime = contentType.split(";")[0]?.trim().toLowerCase() ?? "";
  return mime === "text/html" || mime === "application/xhtml+xml";
}

/** 204/205/304 carry no body; the Response constructor would throw. */
function statusAllowsBody(status: number): boolean {
  return status !== 204 && status !== 205 && status !== 304;
}

/**
 * Finds the page that holds the tunnel.
 *
 * The SW can be killed and restarted, losing the cached client id. So we
 * probe the windows one by one and cache the first that says it has the
 * tunnel.
 */
async function findTunnelClient(): Promise<Client | null> {
  if (tunnelClientId) {
    const cached = await self.clients.get(tunnelClientId);
    if (cached) {
      // Fresh enough: use it. Stale: ask whether it still has the tunnel
      // (the tab can be alive but tunnel-less).
      if (Date.now() - tunnelSeenAt < TUNNEL_TRUST_MS) return cached;
      if (await probe(cached)) {
        tunnelSeenAt = Date.now();
        return cached;
      }
    }
    tunnelClientId = null;
    tunnelSeenAt = 0;
  }

  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const candidate of windows) {
    if (await probe(candidate)) {
      tunnelClientId = candidate.id;
      tunnelSeenAt = Date.now();
      return candidate;
    }
  }
  return null;
}

function probe(client: Client): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => {
      channel.port1.close();
      resolve(false);
    }, PROBE_TIMEOUT_MS);

    channel.port1.onmessage = (ev: MessageEvent<{ type?: string; hasTunnel?: boolean }>) => {
      clearTimeout(timer);
      channel.port1.close();
      resolve(ev.data?.type === "bridge-probe-reply" && ev.data.hasTunnel === true);
    };

    try {
      client.postMessage({ type: "bridge-probe" }, [channel.port2]);
    } catch {
      clearTimeout(timer);
      resolve(false);
    }
  });
}
