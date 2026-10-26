/**
 * Service Worker <-> tunel sayfasi arasindaki mesaj bicimi.
 *
 * Service Worker icinde WebRTC YOK (README §2): WebRTC baglantisini sayfa
 * tutar. SW yakaladigi her `fetch` istegini bir `MessageChannel` uzerinden
 * sayfaya yollar, sayfa cevabi parca parca geri gonderir ve SW bunu
 * `ReadableStream` ile servis eder.
 *
 * Bu dosya hem app (DOM) hem SW (WebWorker) tarafindan import edilir, bu
 * yuzden ortama ozgu hicbir tip kullanmaz.
 */

/** SW'nin sayfadan istek yapmasi. `port` uzerinden cevap beklenir. */
export interface BridgeRequest {
  readonly type: "bridge-req";
  readonly method: string;
  /** Yerel sunucuya gidecek yol: scope oneki cikarilmis, "/" ile baslar. */
  readonly path: string;
  readonly headers: Record<string, string>;
  /** Istek govdesi; yoksa null. Faz 1'de SW govdeyi tamponlar. */
  readonly body: ArrayBuffer | null;
}

/** Sayfanin "tunelin sende mi" sorusuna cevabi. */
export interface BridgeProbe {
  readonly type: "bridge-probe";
}

export interface BridgeProbeReply {
  readonly type: "bridge-probe-reply";
  readonly hasTunnel: boolean;
}

/** Sayfanin kendini tunel sahibi olarak kaydetmesi. */
export interface BridgeTunnelReady {
  readonly type: "bridge-tunnel-ready";
}

/** Sayfanin tunelin kapandigini bildirmesi. */
export interface BridgeTunnelGone {
  readonly type: "bridge-tunnel-gone";
}

export type BridgeToPage = BridgeRequest | BridgeProbe;
export type BridgeToWorker = BridgeTunnelReady | BridgeTunnelGone | BridgeProbeReply;

/** Cevap basligi. */
export interface BridgeResponseHead {
  readonly type: "res";
  readonly status: number;
  readonly headers: Record<string, string>;
}

/** Cevap govdesinden bir parca. `data` transfer edilir, kopyalanmaz. */
export interface BridgeResponseChunk {
  readonly type: "chunk";
  readonly data: ArrayBuffer;
}

/** Govde bitti. */
export interface BridgeResponseEnd {
  readonly type: "end";
}

/** Istek basarisiz. */
export interface BridgeResponseError {
  readonly type: "error";
  readonly message: string;
}

export type BridgeResponse =
  | BridgeResponseHead
  | BridgeResponseChunk
  | BridgeResponseEnd
  | BridgeResponseError;

/**
 * SW'nin tamponlayacagi en buyuk istek govdesi.
 *
 * Faz 1 sinirlamasi: istek govdesi akitilmiyor, SW'de tamamen tamponlaniyor.
 * Ajanin --max-body varsayilani 50MB; SW tarafinda daha dusuk bir sinir
 * tutuyoruz cunku govde tarayici belleginde duruyor.
 */
export const MAX_BUFFERED_REQUEST_BODY = 16 * 1024 * 1024;

/** Sayfanin probe'a cevap vermesi icin beklenecek sure. */
export const PROBE_TIMEOUT_MS = 1000;
