/// <reference lib="webworker" />

/**
 * PeerPages Service Worker — elle yazilmis, elle derlenir
 * (BRIEF karari: vite-plugin-pwa kullanilmaz).
 *
 * Gorevi: kendi kapsamindaki TUM `fetch` isteklerini yakalayip tunel sayfasina
 * iletmek ve cevabi `ReadableStream` ile akitmak. Boylece goreli link, CSS,
 * JS, gorsel ve fetch/XHR seffaf calisir (BRIEF §2).
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

/** O client'in tuneli oldugunu en son dogruladigimiz an. */
let tunnelSeenAt = 0;

/**
 * Onbellekteki client id'nin yeniden yoklanmadan guvenilecegi sure.
 *
 * Neden gerekli: `clients.get(id)` sekme HAYATTA oldugu surece bir client
 * dondurur — tuneli kaybetmis olsa bile. Sayfanin pagehide'da gonderdigi
 * "tunel gitti" haberi ise EN IYI CABADIR: sekme cokerse, oldurulurse ya da
 * sayfa gezinirse hic gelmez. Bu sure sonunda yeniden probe ediyoruz ki olu
 * bir tunele istek yonlendirmeyelim.
 *
 * Tespit TEMBELDIR: istek gelmezse kontrol de olmaz. Zaman sinirli gercek
 * koruma ajan tarafindaki bosta kalma denetimi (agent/internal/tunnel).
 * SW'de periyodik zamanlayici guvenilmez: worker bostayken oldurulur.
 */
const TUNNEL_TRUST_MS = 5000;

/**
 * Sayfa "yeniden baglaniyorum" dedi ve bu ana kadar bekletmeye deger
 * (Faz 4). 0 ise bekletme yok.
 */
let reconnectingUntil = 0;

/** Tunelin donmesini bekleyen istekler. */
let tunnelWaiters: Array<() => void> = [];

/** Tunel geri geldi: bekleyen istekleri birlikte serbest birak. */
function releaseTunnelWaiters(): void {
  const waiters = tunnelWaiters;
  tunnelWaiters = [];
  for (const w of waiters) w();
}

/**
 * Tunelin donmesini en fazla `ms` kadar bekler.
 *
 * Fetch olayi cevabi beklerken worker hayatta kalir, bu yuzden burada
 * zamanlayici guvenilir — SW'de PERIYODIK zamanlayicinin guvenilmez olmasi
 * ayri bir mesele (worker bostayken oldurulur).
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
      // Shim'in WebSocket istegi (Faz 6): tunel sahibini bul ve portu ona
      // AKTAR. Sonrasi sayfa <-> sayfa; SW bosta oldurulebilir, uzun
      // yasayan bir soket ona dayanamaz.
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
 * Shim'in ws istegini tunel sahibi sayfaya iletir.
 *
 * Tunel yoksa portu 1013 ile kapatir: shim bunu `onclose` olarak gorur ve
 * sayfa kendi yeniden deneme mantigini isletir (Vite ve Socket.IO ikisi de
 * yeniden dener).
 */
async function forwardWsOpen(
  path: string,
  protos: readonly string[],
  port: MessagePort,
): Promise<void> {
  const client = await findTunnelClient();
  if (!client) {
    port.postMessage({ type: "ws-close", code: 1013, reason: "tunel yok" });
    port.close();
    return;
  }
  try {
    client.postMessage({ type: "bridge-ws-open-forward", path, protos }, [port]);
  } catch {
    port.postMessage({ type: "ws-close", code: 1011, reason: "kopru kurulamadi" });
    port.close();
  }
}

self.addEventListener("fetch", (event: FetchEvent) => {
  const request = event.request;

  // Yalnizca kendi kapsamimizdaki istekler (BRIEF guvenlik ilkesi 5).
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (!url.pathname.startsWith(SCOPE_PATH)) return;

  // App shell kendi dosyalarini tunelden GECIRMEZ: aksi halde baglanti
  // ekranini acmak icin tunele ihtiyac duyardik (yumurta-tavuk).
  if (isAppShellPath(url.pathname, SCOPE_PATH)) return;

  event.respondWith(handle(request, url));
});

async function handle(request: Request, url: URL): Promise<Response> {
  let client = await findTunnelClient();

  // Sayfa yeniden baglanmaya calisiyorsa istegi hemen reddetmiyoruz:
  // uyku/uyanmadan sonra site kirilmak yerine yavaslamis gorunur.
  if (!client && Date.now() < reconnectingUntil) {
    const waitMs = request.mode === "navigate" ? RECONNECT_WAIT_NAVIGATE_MS : RECONNECT_WAIT_MS;
    await waitForTunnel(waitMs);
    client = await findTunnelClient();
  }

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

  let response = await forward(client, request, url, body);

  // Tunel tam bu istek sirasinda koptuysa sayfa "retryable" hata doner:
  // istek hedefe hic gitmemistir. Yeniden baglanmayi kisa sure bekleyip
  // BIR KEZ tekrar deniyoruz — yalnizca idempotent isteklerde, cunku
  // POST'un hedefe ulasip ulasmadigini buradan bilemeyiz.
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
    return new Response("peerpages: tunel acik degil\n", {
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
 * forward'in "tunel yoktu, istek gitmedi" cevabi.
 *
 * Response yerine ayri bir deger: cagiran taraf bunu tekrar deneyip
 * deneyemeyecegine karar verir.
 */
const RETRY_TUNNEL = Symbol("retry-tunnel");

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
 * geldikce akisa yazilir (BRIEF Faz 1).
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
          // Zaten kapanmis olabilir.
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

/**
 * Cevabi kurar ve gerekiyorsa WebSocket shim'ini enjekte eder (Faz 6).
 *
 * Enjeksiyon YALNIZCA iki kosul birlikte saglanirsa: ajan
 * `x-peerpages-ws-shim` isaretini koymus (yani `--inject-ws-shim` verilmis)
 * VE cevap HTML. Baska hicbir cevaba dokunulmaz.
 *
 * Isaret ziyaretciye GITMEZ: ajanla aramizdaki bir detay, sayfanin gormesi
 * gereken bir sey degil.
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

/** content-type HTML mi (parametreler yok sayilir). */
function isHTMLResponse(contentType: string | null): boolean {
  if (!contentType) return false;
  const mime = contentType.split(";")[0]?.trim().toLowerCase() ?? "";
  return mime === "text/html" || mime === "application/xhtml+xml";
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
    if (cached) {
      // Taze ise oldugu gibi kullan; bayatladiysa gercekten tuneli var mi
      // diye sor (sekme hayatta ama tunelsiz olabilir).
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
