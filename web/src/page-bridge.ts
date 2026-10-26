/**
 * Sayfa tarafindaki koprunun Service Worker ucu.
 *
 * Service Worker yakaladigi istekleri `MessageChannel` ile buraya yollar;
 * burada tunele cevrilir ve cevap parca parca geri gonderilir. SW cevabi
 * `ReadableStream` ile servis eder, yani tam govde beklenmez.
 */

import type { BridgeRequest, BridgeResponse, BridgeToPage } from "./bridge";
import type { Tunnel } from "./tunnel";

export interface BridgeHooks {
  /** Her istek tamamlandiginda cagrilir (durum ekranindaki sayaclar icin). */
  onRequestDone?: (info: { path: string; status: number; bytes: number }) => void;
  onRequestError?: (info: { path: string; message: string }) => void;
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
    send({ type: "error", message: "tunel acik degil" });
    port.close();
    hooks.onRequestError?.({ path: msg.path, message: "tunel acik degil" });
    return;
  }

  let bytes = 0;
  try {
    const res = await tunnel.request({
      method: msg.method,
      path: msg.path,
      headers: msg.headers,
      body: msg.body ? new Uint8Array(msg.body) : null,
    });

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
    send({ type: "error", message });
    port.close();
    hooks.onRequestError?.({ path: msg.path, message });
  }
}
