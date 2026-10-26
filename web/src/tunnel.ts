/**
 * Sayfa tarafindaki tunel: ctrl/data kanallari uzerinden istek coklama.
 *
 * Service Worker'in yakaladigi her `fetch`, sayfaya iletilir ve burada bir
 * `req` mesajina cevrilir. Cevap akis halinde geri verilir
 * (`ReadableStream`), tam govde beklenmez.
 *
 * Kanallar arasinda siralama YOK (protocol/README.md):
 *  - cevap cerceveleri `res` mesajindan once gelebilir → istek kaydi `req`
 *    yollanirken olusturulur, cerceveler her zaman bir yere dusurulur;
 *  - `end` cercevelerden once gelebilir → akis, `end`'teki `frames`/`len`
 *    sayaclari tamamlanmadan kapatilmaz.
 */

import {
  type AbortMsg,
  type CtrlMsg,
  canonicalCtrl,
  type EndMsg,
  parseCtrl,
  type ResMsg,
} from "./protocol/ctrl";
import { decodeFrame, encodeFrame, MAX_PAYLOAD } from "./protocol/framing";

/** Backpressure esikleri — ajan tarafiyla ayni (protocol/README.md). */
const BUFFERED_HIGH = 1 << 20; // 1 MiB
const BUFFERED_LOW = 256 << 10; // 256 KiB

/** `res` mesajinin bu sure icinde gelmesi beklenir. */
const RESPONSE_TIMEOUT_MS = 30_000;

/** uint32 sinirinda donen istek id'si. */
const MAX_ID = 0xffffffff;

export class TunnelError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TunnelError";
  }
}

export interface TunnelResponse {
  readonly status: number;
  readonly headers: Readonly<Record<string, string>>;
  readonly body: ReadableStream<Uint8Array>;
}

export interface RequestInit_ {
  readonly method: string;
  readonly path: string;
  readonly headers: Record<string, string>;
  /** Istek govdesi; yoksa null. */
  readonly body?: ReadableStream<Uint8Array> | Uint8Array | null;
  readonly signal?: AbortSignal;
}

export interface TunnelStats {
  readonly requests: number;
  readonly bytesIn: number;
  readonly bytesOut: number;
  readonly inflight: number;
}

/**
 * Gelen govde parcalarini bir ReadableStream'e ceviren kuyruk.
 *
 * `pull` tabanli: tuketici okudukca kuyruktan verilir, boylece Service
 * Worker cevabi akitirken bellekte govde birikmesi sinirli kalir.
 */
class BodyQueue {
  private readonly chunks: Uint8Array[] = [];
  private done = false;
  private failure: Error | null = null;
  private wake: (() => void) | null = null;

  readonly stream: ReadableStream<Uint8Array>;

  constructor() {
    this.stream = new ReadableStream<Uint8Array>({
      pull: async (controller) => {
        for (;;) {
          const chunk = this.chunks.shift();
          if (chunk !== undefined) {
            controller.enqueue(chunk);
            return;
          }
          if (this.failure) {
            controller.error(this.failure);
            return;
          }
          if (this.done) {
            controller.close();
            return;
          }
          await new Promise<void>((resolve) => {
            this.wake = resolve;
          });
        }
      },
      cancel: () => {
        this.chunks.length = 0;
      },
    });
  }

  push(chunk: Uint8Array): void {
    if (this.done || this.failure) return;
    this.chunks.push(chunk);
    this.notify();
  }

  close(): void {
    if (this.done || this.failure) return;
    this.done = true;
    this.notify();
  }

  fail(err: Error): void {
    if (this.done || this.failure) return;
    this.failure = err;
    this.notify();
  }

  private notify(): void {
    const w = this.wake;
    this.wake = null;
    if (w) w();
  }
}

/** Bekleyen bir istegin durumu. */
interface Pending {
  readonly id: number;
  readonly body: BodyQueue;
  /** Alinan cerceve sayisi; beklenen sonraki seq ile ayni. */
  frames: number;
  bytes: number;
  endSeen: boolean;
  expectFrames: number;
  expectLen: number;
  settled: boolean;
  resolve: (res: TunnelResponse) => void;
  reject: (err: Error) => void;
  timer: ReturnType<typeof setTimeout> | null;
  onAbortSignal: (() => void) | null;
  signal?: AbortSignal | undefined;
}

export class Tunnel {
  private readonly ctrl: RTCDataChannel;
  private readonly data: RTCDataChannel;
  private readonly pending = new Map<number, Pending>();
  private nextId = 1;
  private closed = false;

  private requests = 0;
  private bytesIn = 0;
  private bytesOut = 0;

  private pongWaiters: Array<() => void> = [];

  /** Ajandan gelen `hello` mesaji (hedef listesi) — gelince doldurulur. */
  hello: Extract<CtrlMsg, { t: "hello" }> | null = null;

  constructor(ctrl: RTCDataChannel, data: RTCDataChannel) {
    this.ctrl = ctrl;
    this.data = data;

    this.data.binaryType = "arraybuffer";
    this.data.bufferedAmountLowThreshold = BUFFERED_LOW;

    this.ctrl.addEventListener("message", (ev) => {
      this.onCtrl(ev as MessageEvent<string>);
    });
    this.data.addEventListener("message", (ev) => {
      this.onData(ev as MessageEvent<ArrayBuffer>);
    });

    const onClose = () => {
      this.closeAll(new TunnelError("tunel kapandi"));
    };
    this.ctrl.addEventListener("close", onClose);
    this.data.addEventListener("close", onClose);
    this.ctrl.addEventListener("error", onClose);
    this.data.addEventListener("error", onClose);
  }

  get stats(): TunnelStats {
    return {
      requests: this.requests,
      bytesIn: this.bytesIn,
      bytesOut: this.bytesOut,
      inflight: this.pending.size,
    };
  }

  get isOpen(): boolean {
    return !this.closed && this.ctrl.readyState === "open" && this.data.readyState === "open";
  }

  /** Tuneli kapatir ve bekleyen istekleri hata ile sonlandirir. */
  close(): void {
    this.closeAll(new TunnelError("tunel kapatildi"));
  }

  /** ctrl kanalinda ping/pong gidis donus suresini olcer (ms). */
  async ping(timeoutMs = 5000): Promise<number> {
    if (!this.isOpen) throw new TunnelError("tunel acik degil");

    const started = performance.now();
    const waiter = new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pongWaiters = this.pongWaiters.filter((w) => w !== onPong);
        reject(new TunnelError("pong zaman asimi"));
      }, timeoutMs);
      const onPong = () => {
        clearTimeout(timer);
        resolve();
      };
      this.pongWaiters.push(onPong);
    });

    this.sendCtrl({ t: "ping" });
    await waiter;
    return performance.now() - started;
  }

  /**
   * Tunel uzerinden bir istek yapar.
   *
   * Donen promise `res` mesaji gelince cozulur; govde akis halinde gelir.
   */
  async request(init: RequestInit_): Promise<TunnelResponse> {
    if (!this.isOpen) throw new TunnelError("tunel acik degil");
    if (init.signal?.aborted) throw new TunnelError("istek iptal edildi");

    const id = this.takeId();
    const hasBody = init.body != null;
    const bodyQueue = new BodyQueue();

    const entry: Pending = {
      id,
      body: bodyQueue,
      frames: 0,
      bytes: 0,
      endSeen: false,
      expectFrames: 0,
      expectLen: 0,
      settled: false,
      resolve: () => {},
      reject: () => {},
      timer: null,
      onAbortSignal: null,
      signal: init.signal,
    };

    const promise = new Promise<TunnelResponse>((resolve, reject) => {
      entry.resolve = resolve;
      entry.reject = reject;
    });

    entry.timer = setTimeout(() => {
      this.failPending(id, new TunnelError("cevap zaman asimi (res gelmedi)"));
    }, RESPONSE_TIMEOUT_MS);

    if (init.signal) {
      const onAbort = () => {
        this.abortRequest(id, "client-cancel");
      };
      entry.onAbortSignal = onAbort;
      init.signal.addEventListener("abort", onAbort, { once: true });
    }

    this.pending.set(id, entry);
    this.requests += 1;

    // Kayit kurulduktan SONRA `req` yollanir: cevap cerceveleri hemen gelse
    // bile dusecek bir yer var.
    this.sendCtrl({
      t: "req",
      id,
      method: init.method.toUpperCase(),
      path: init.path,
      headers: init.headers,
      hasBody,
    });

    if (hasBody) {
      // Govdeyi arka planda akit; cagiran `res`'i beklemeye devam eder.
      void this.sendBody(id, init.body as ReadableStream<Uint8Array> | Uint8Array);
    }

    return promise;
  }

  // --- gonderim ---

  private sendCtrl(msg: CtrlMsg): void {
    if (this.ctrl.readyState !== "open") {
      throw new TunnelError(`ctrl kanali acik degil (${this.ctrl.readyState})`);
    }
    this.ctrl.send(canonicalCtrl(msg));
  }

  private async sendBody(id: number, body: ReadableStream<Uint8Array> | Uint8Array): Promise<void> {
    let seq = 0;
    let total = 0;

    const sendChunk = async (chunk: Uint8Array): Promise<void> => {
      for (let off = 0; off < chunk.byteLength; off += MAX_PAYLOAD) {
        const end = Math.min(off + MAX_PAYLOAD, chunk.byteLength);
        const slice = chunk.subarray(off, end);

        await this.waitWritable();
        if (this.data.readyState !== "open") {
          throw new TunnelError("data kanali kapandi");
        }
        const frame = encodeFrame(id, seq, slice);
        this.data.send(frame);
        seq += 1;
        total += slice.byteLength;
        this.bytesOut += slice.byteLength;
      }
    };

    try {
      if (body instanceof Uint8Array) {
        await sendChunk(body);
      } else {
        const reader = body.getReader();
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          if (value && value.byteLength > 0) {
            await sendChunk(value);
          }
        }
      }
      // frames/len ZORUNLU: alici govdenin bittigini bu sayaclarla anlar.
      this.sendCtrl({ t: "end", id, frames: seq, len: total });
    } catch (err) {
      this.abortRequest(id, "client-body-error");
      throw err instanceof Error ? err : new Error(String(err));
    }
  }

  /** data kanalinin gonderim tamponu bosalana kadar bekler. */
  private waitWritable(): Promise<void> {
    if (this.data.bufferedAmount <= BUFFERED_HIGH) {
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      const onLow = () => {
        this.data.removeEventListener("bufferedamountlow", onLow);
        resolve();
      };
      this.data.addEventListener("bufferedamountlow", onLow);
    });
  }

  private abortRequest(id: number, reason: string): void {
    const entry = this.pending.get(id);
    if (!entry) return;
    try {
      this.sendCtrl({ t: "abort", id, reason });
    } catch {
      // Kanal zaten kapali olabilir; akisi yine de sonlandir.
    }
    this.failPending(id, new TunnelError(`istek iptal edildi: ${reason}`));
  }

  // --- alim ---

  private onCtrl(ev: MessageEvent<string>): void {
    let msg: CtrlMsg;
    try {
      msg = parseCtrl(typeof ev.data === "string" ? ev.data : String(ev.data));
    } catch (err) {
      // Ajan bozuk mesaj yollamamali; tuneli dusurmeden gecelim.
      console.warn("[peerpages] gecersiz ctrl mesaji:", err);
      return;
    }

    switch (msg.t) {
      case "res":
        this.onRes(msg);
        break;
      case "end":
        this.onEnd(msg);
        break;
      case "abort":
        this.onAbort(msg);
        break;
      case "hello":
        this.hello = msg;
        break;
      case "ping":
        this.sendCtrl({ t: "pong" });
        break;
      case "pong": {
        const waiters = this.pongWaiters;
        this.pongWaiters = [];
        for (const w of waiters) w();
        break;
      }
      case "req":
        console.warn("[peerpages] ajandan req geldi, protokol ihlali");
        break;
    }
  }

  private onRes(msg: ResMsg): void {
    const entry = this.pending.get(msg.id);
    if (!entry || entry.settled) return;

    entry.settled = true;
    if (entry.timer !== null) {
      clearTimeout(entry.timer);
      entry.timer = null;
    }
    entry.resolve({
      status: msg.status,
      headers: msg.headers,
      body: entry.body.stream,
    });
    // `end` zaten gelmis ve sayaclar tamamsa akisi hemen kapat.
    this.checkComplete(entry);
  }

  private onEnd(msg: EndMsg): void {
    const entry = this.pending.get(msg.id);
    if (!entry) return;
    entry.endSeen = true;
    entry.expectFrames = msg.frames;
    entry.expectLen = msg.len;
    this.checkComplete(entry);
  }

  private onAbort(msg: AbortMsg): void {
    this.failPending(msg.id, new TunnelError(`ajan akisi iptal etti: ${msg.reason}`));
  }

  private onData(ev: MessageEvent<ArrayBuffer>): void {
    let frame: ReturnType<typeof decodeFrame>;
    try {
      const raw = ev.data;
      const bytes =
        raw instanceof ArrayBuffer ? new Uint8Array(raw) : new Uint8Array(raw as ArrayBuffer);
      frame = decodeFrame(bytes);
    } catch (err) {
      console.warn("[peerpages] gecersiz data cercevesi:", err);
      return;
    }

    const entry = this.pending.get(frame.id);
    if (!entry) {
      // Istek cozulmus ya da iptal edilmis: cerceveyi dusur.
      return;
    }

    // Kanal ordered: beklenmeyen seq protokol hatasi.
    if (frame.seq !== entry.frames) {
      this.abortRequest(frame.id, "bad-seq");
      return;
    }
    entry.frames += 1;
    entry.bytes += frame.payload.byteLength;
    this.bytesIn += frame.payload.byteLength;

    if (frame.payload.byteLength > 0) {
      entry.body.push(frame.payload);
    }
    this.checkComplete(entry);
  }

  /**
   * `end` geldiyse VE sayaclar tamamlandiysa akisi kapatir.
   *
   * Sayac kontrolu sart: ctrl ile data arasinda siralama olmadigi icin `end`
   * cercevelerden once gelebilir (protocol/README.md).
   */
  private checkComplete(entry: Pending): void {
    if (!entry.endSeen) return;

    if (entry.frames > entry.expectFrames || entry.bytes > entry.expectLen) {
      this.abortRequest(entry.id, "body-length-mismatch");
      return;
    }
    if (entry.frames < entry.expectFrames || entry.bytes < entry.expectLen) {
      return; // eksik cerceve bekleniyor
    }

    entry.body.close();
    this.cleanup(entry);
  }

  private failPending(id: number, err: Error): void {
    const entry = this.pending.get(id);
    if (!entry) return;

    if (!entry.settled) {
      entry.settled = true;
      entry.reject(err);
    }
    entry.body.fail(err);
    this.cleanup(entry);
  }

  private cleanup(entry: Pending): void {
    if (entry.timer !== null) {
      clearTimeout(entry.timer);
      entry.timer = null;
    }
    if (entry.signal && entry.onAbortSignal) {
      entry.signal.removeEventListener("abort", entry.onAbortSignal);
      entry.onAbortSignal = null;
    }
    this.pending.delete(entry.id);
  }

  private closeAll(err: Error): void {
    if (this.closed) return;
    this.closed = true;
    for (const id of [...this.pending.keys()]) {
      this.failPending(id, err);
    }
    const waiters = this.pongWaiters;
    this.pongWaiters = [];
    for (const w of waiters) w();
  }

  private takeId(): number {
    // uint32 sinirinda don; kullanimda olan id'yi atla.
    for (let i = 0; i <= MAX_ID; i++) {
      const id = this.nextId;
      this.nextId = this.nextId >= MAX_ID ? 1 : this.nextId + 1;
      if (!this.pending.has(id)) return id;
    }
    throw new TunnelError("bos istek id'si kalmadi");
  }
}
