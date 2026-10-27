/**
 * Sayfa tarafindaki koprunun Service Worker ucu.
 *
 * Service Worker yakaladigi istekleri `MessageChannel` ile buraya yollar;
 * burada tunele cevrilir ve cevap parca parca geri gonderilir. SW cevabi
 * `ReadableStream` ile servis eder, yani tam govde beklenmez.
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
  /** Her istek tamamlandiginda cagrilir (durum ekranindaki sayaclar icin). */
  onRequestDone?: (info: { path: string; status: number; bytes: number }) => void;
  onRequestError?: (info: { path: string; message: string }) => void;
  /** Acik ws oturumu sayisi degisti (Faz 6, durum ekrani icin). */
  onSocketChange?: (open: number) => void;
}

/**
 * SW mesajlarini dinlemeye baslar.
 *
 * `getTunnel` cagri aninda aktif tuneli dondurur (ya da null): tunel yeniden
 * kurulabilir, bu yuzden referans degil fonksiyon aliyoruz.
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

/** Sayfanin kendini tunel sahibi olarak kaydetmesi. */
export function announceTunnelReady(): void {
  navigator.serviceWorker?.controller?.postMessage({ type: "bridge-tunnel-ready" });
}

/** Tunelin kapandigini SW'ye bildirir. */
export function announceTunnelGone(): void {
  navigator.serviceWorker?.controller?.postMessage({ type: "bridge-tunnel-gone" });
}

/**
 * Tunelin koptugunu ama yeniden baglanildigini bildirir (Faz 4).
 *
 * SW bu haberi aldiktan sonra istekleri kisa bir sure bekletir; boylece
 * ziyaretcinin gezdigi site kopma aninda 503 yemek yerine yeniden
 * baglanmayi bekler.
 */
export function announceTunnelReconnecting(): void {
  navigator.serviceWorker?.controller?.postMessage({ type: "bridge-tunnel-reconnecting" });
}

/**
 * Shim'in ws oturumunu tunele baglar (Faz 6).
 *
 * Port iki yone pompalaniyor: shim'den gelen `ws-send` tunele, tunelden
 * gelen mesaj shim'e. SW bu noktadan sonra devrede DEGIL.
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
    fail(1013, "tunel acik degil");
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
        // Kopyalamadan transfer et (istek govdelerindeki ayni gerekce):
        // dilimin tamponu paylasilan olabilir, kendi tamponunu cikariyoruz.
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
        // Gonderim hatasi oturumu zaten kapatir; burada yutuyoruz ki
        // yakalanmamis promise reddi olusmasin.
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
    // Istek hedefe hic gitmedi: SW yeniden baglanmayi bekleyip tekrar
    // deneyebilir.
    send({ type: "error", message: "tunel acik degil", retryable: true });
    port.close();
    hooks.onRequestError?.({ path: msg.path, message: "tunel acik degil" });
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

      // Kopyalamadan transfer et: 5 MB'lik indirmede fark ediyor.
      // Not: value'nun tamponu paylasilan olabilir, bu yuzden dilimin
      // kendi tamponunu cikariyoruz.
      const copy = value.slice().buffer;
      send({ type: "chunk", data: copy }, [copy]);
    }

    send({ type: "end" });
    port.close();
    hooks.onRequestDone?.({ path: msg.path, status: res.status, bytes });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    // Cevap basligi hic gitmediyse VE tunel artik kapaliysa, istek tunel
    // koptugu icin dustu: SW yeniden baglanmayi bekleyip tekrar
    // deneyebilir. Hedeften gelen hatalar (500 vb.) basligi gonderdigi
    // icin bu yola girmez.
    const retryable = !headSent && !tunnel.isOpen;
    send({ type: "error", message, retryable });
    port.close();
    hooks.onRequestError?.({ path: msg.path, message });
  }
}
