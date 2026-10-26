/// <reference lib="webworker" />

/**
 * PeerPages Service Worker — elle yazilmis, elle derlenir
 * (README karari: vite-plugin-pwa kullanilmaz).
 *
 * Gorevi: kendi kapsamindaki TUM `fetch` isteklerini yakalayip tunel sayfasina
 * iletmek ve cevabi `ReadableStream` ile akitmak. Boylece goreli link, CSS,
 * JS, gorsel ve fetch/XHR seffaf calisir (README §2).
 *
 * Sinirlar (bilincli):
 *  - SW icinde WebRTC yok → baglantiyi sayfa tutar, SW yalnizca koprudur.
 *  - Tunel sayfasi kapaliysa navigasyon baglanti ekranina yonlendirilir,
 *    alt kaynaklar 503 doner.
 *  - Istek govdesi akitilmaz, tamponlanir (bridge.ts).
 */

import { isAppShellPath } from "./base";
import {
  type BridgeResponse,
  type BridgeToWorker,
  MAX_BUFFERED_REQUEST_BODY,
  PROBE_TIMEOUT_MS,
} from "./bridge";

declare const self: ServiceWorkerGlobalScope;

/**
 * SW'nin kapsami — DERLEME ZAMANI degil, CALISMA ZAMANI belirlenir.
 *
 * `import.meta.env.BASE_URL` kullanmiyoruz cunku SW ayri bir vite
 * derlemesinden gecer ve iki derlemenin `base` degeri kolayca birbirinden
 * ayrilabilir (bir kez ayrildi: SW yolun onekini yanlis kirpti ve yerel
 * sunucuya /PeerPages/index.html gitti, 404). `registration.scope` her zaman
 * dogru: SW'yi sayfa hangi scope ile kaydettiyse o.
 */
const SCOPE_PATH = new URL(self.registration.scope).pathname;

/** Tunelin bulundugu sayfanin client id'si; SW yeniden baslarsa yeniden bulunur. */
let tunnelClientId: string | null = null;

self.addEventListener("install", () => {
  // Yeni SW'yi hemen devreye al: kullanici sekmeyi kapatip acmak zorunda kalmasin.
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
      }
      break;
    }
    case "bridge-tunnel-gone":
      tunnelClientId = null;
      break;
    default:
      break;
  }
});

self.addEventListener("fetch", (event: FetchEvent) => {
  const request = event.request;

  // Yalnizca kendi kapsamimizdaki istekler (README guvenlik ilkesi 5).
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (!url.pathname.startsWith(SCOPE_PATH)) return;

  // App shell kendi dosyalarini tunelden GECIRMEZ: aksi halde baglanti
  // ekranini acmak icin tunele ihtiyac duyardik (yumurta-tavuk).
  if (isAppShellPath(url.pathname, SCOPE_PATH)) return;

  event.respondWith(handle(request, url));
});

async function handle(request: Request, url: URL): Promise<Response> {
  const client = await findTunnelClient();

  if (!client) {
    if (request.mode === "navigate") {
      // Baglanti ekranina don; connect/ tunelden gecmedigi icin acilir.
      return Response.redirect(
        new URL(`${SCOPE_PATH}connect/`, self.location.origin).toString(),
        302,
      );
    }
    return new Response("peerpages: tunel acik degil\n", {
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
      return new Response(`peerpages: istek govdesi okunamadi (${String(err)})\n`, {
        status: 413,
        headers: { "content-type": "text/plain; charset=utf-8" },
      });
    }
  }

  return forward(client, request, url, body);
}

/** Istek govdesini sinirli boyutta tamponlar. */
async function readLimitedBody(request: Request): Promise<ArrayBuffer | null> {
  const buf = await request.clone().arrayBuffer();
  if (buf.byteLength === 0) return null;
  if (buf.byteLength > MAX_BUFFERED_REQUEST_BODY) {
    throw new Error(`govde cok buyuk: ${buf.byteLength} bayt`);
  }
  return buf;
}

/**
 * Istegi tunel sayfasina iletir ve cevabi akis halinde dondurur.
 *
 * Tam govde beklenmez: `res` gelir gelmez Response olusturulur, parcalar
 * geldikce akisa yazilir (README Faz 1).
 */
function forward(
  client: Client,
  request: Request,
  url: URL,
  body: ArrayBuffer | null,
): Promise<Response> {
  return new Promise<Response>((resolve, reject) => {
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
          // Zaten kapanmis olabilir.
        }
      }
      channel.port1.close();
    };

    const fail = (message: string) => {
      if (!settled) {
        settled = true;
        channel.port1.close();
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
        case "res":
          if (settled) return;
          settled = true;
          resolve(
            new Response(statusAllowsBody(msg.status) ? stream : null, {
              status: msg.status,
              headers: msg.headers,
            }),
          );
          break;
        case "chunk":
          enqueue(new Uint8Array(msg.data));
          break;
        case "end":
          finish();
          break;
        case "error":
          fail(msg.message);
          break;
      }
    };

    const headers: Record<string, string> = {};
    request.headers.forEach((value, name) => {
      headers[name.toLowerCase()] = value;
    });

    // Scope oneki cikarilir: yerel sunucu "/index.html" bekler,
    // ziyaretci "/PeerPages/index.html" istemistir.
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

/** 204/205/304 govde tasiyamaz; aksi halde Response kurucusu hata verir. */
function statusAllowsBody(status: number): boolean {
  return status !== 204 && status !== 205 && status !== 304;
}

/**
 * Tunelin bulundugu sayfayi bulur.
 *
 * SW oldurulup yeniden baslatilabilir; o zaman hafizadaki client id kaybolur.
 * Bu yuzden pencereler tek tek yoklanir ve ilk "tunelim var" diyen onbellege
 * alinir.
 */
async function findTunnelClient(): Promise<Client | null> {
  if (tunnelClientId) {
    const cached = await self.clients.get(tunnelClientId);
    if (cached) return cached;
    tunnelClientId = null;
  }

  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const candidate of windows) {
    if (await probe(candidate)) {
      tunnelClientId = candidate.id;
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
