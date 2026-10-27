/**
 * `window.WebSocket` shim'i — tunelden gecen sayfalara enjekte edilir (Faz 6).
 *
 * Neden gerekli: Service Worker yalnizca `fetch` olaylarini yakalar, WebSocket
 * el sikismasi onun kapsaminda DEGIL. Shim olmadan tunelden gecen bir sayfanin
 * `new WebSocket(...)` cagrisi gercek aga gider ve basarisiz olur — yani Vite
 * HMR, Socket.IO ve canli onizleme yapan her dev sunucusu yarim calisir.
 *
 * Yol: shim ziyaretci sekmesinde kosar, tunel BASKA sekmededir. Bir
 * `MessageChannel` kurulup portu SW'ye yollanir, SW onu tunel sahibi sayfaya
 * AKTARIR; sonrasi sayfa <-> sayfa (SW bosta oldurulebilir).
 *
 * Bu dosya KLASIK script olarak derlenir (modul degil) ve sayfanin kendi
 * script'lerinden ONCE kosmali.
 */

import { WS_SHIM_FILE, type WsPortToPage, type WsPortToShim } from "./bridge";

type Listener = ((ev: Event) => void) | null;

const CONNECTING = 0 as const;
const OPEN = 1 as const;
const CLOSING = 2 as const;
const CLOSED = 3 as const;

/** WebSocket.readyState'in tip-guvenli hali. */
type ReadyState = typeof CONNECTING | typeof OPEN | typeof CLOSING | typeof CLOSED;

/** Shim'in zaten kurulu oldugunu isaretler (iki kez enjekte edilebilir). */
const INSTALLED = "__peerpagesWsShim";

/**
 * Shim'in servis edildigi dizin = app shell'in scope koku.
 *
 * `document.currentScript` ile hesapliyoruz: enjekte edilen hicbir deger yok,
 * yani ajanin ya da SW'nin scope onekini bilmesi gerekmiyor.
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
 * Bu URL tunelden mi gecmeli?
 *
 * Kural: yalnizca AYNI HOSTNAME (port farkli olabilir). Baska her adres
 * gercek `WebSocket`'e gider — kendi kapsamimizin disina dokunmuyoruz
 * (README ilke 5'in shim karsiligi).
 *
 * Port neden onemsiz: Vite HMR adresine kendi portunu koyabiliyor
 * (`wss://host:5173/`), ama o port ziyaretcinin tarayicisinda anlamsiz;
 * hedefi ajandaki allowlist belirler.
 */
export function isTunneled(u: URL, hostname: string): boolean {
  return u.hostname === hostname;
}

/**
 * Yerel sunucuya gidecek yolu uretir.
 *
 * Scope oneki varsa cikarilir (`/PeerPages/ws` -> `/ws`). Yoksa yol oldugu
 * gibi kullanilir: kok-mutlak bir ws adresi (`wss://host/`) tunelden gecen
 * sayfanin kendi sitesine aittir — shim yalnizca o sayfalara enjekte edilir.
 * Vite HMR tam bu durumda (`__HMR_BASE__` "/" olur).
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
    /** Gonderim zinciri: Blob okumasi asenkron, sira bozulmasin. */
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
        // SW yoksa tunel de yok: hemen kapan, sayfanin kendi yeniden
        // denemesi calissin.
        this.fail(1011, "peerpages: service worker yok");
        return;
      }
      controller.postMessage({ type: "bridge-ws-open", path: localPath(abs, scope), protos }, [
        channel.port2,
      ]);
    }

    /** `bufferedAmount` her zaman 0: gercek tampon tunelin icinde. */
    get bufferedAmount(): number {
      return 0;
    }

    send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
      if (this.readyState === CONNECTING) {
        // Gercek WebSocket de boyle davranir.
        throw new DOMException("WebSocket henuz acilmadi", "InvalidStateError");
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
      // Kapanisi ajanin onaylamasini beklemiyoruz: gercek WebSocket de
      // `close()` sonrasi CLOSING'e gecip onayla CLOSED oluyor.
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

    /** Baglanti hic kurulmadan dustu. */
    private fail(code: number, reason: string): void {
      // Sirali olmasi onemli: gercek WebSocket de error'u close'dan once
      // yollar ve ikisi de mikrotask sonrasi gelir.
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
          console.error("[peerpages] ws dinleyicisi hata verdi:", err);
        }
      }
      this.dispatchEvent(ev);
    }
  }

  // Tunele girmeyen adresler GERCEK WebSocket'e gider.
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

/** Gonderilecek veriyi (binary, ArrayBuffer) ikilisine cevirir. */
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
    // Gorunumun KENDI tamponunu cikar: transfer listesine paylasilan bir
    // tampon koyulamaz.
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

// Testler bu dosyayi node ortaminda import edebilsin (saf yardimcilar
// disari aciliyor); tarayicida her zaman kurulur.
if (typeof window !== "undefined" && typeof document !== "undefined") {
  install();
}
