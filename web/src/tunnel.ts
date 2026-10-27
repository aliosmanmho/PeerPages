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
import { decodeWsPayload, encodeWsPayload, MAX_WS_PAYLOAD } from "./protocol/wsframe";

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

/** Tunel uzerindeki bir WebSocket oturumu (Faz 6). */
export interface TunnelSocket {
  readonly id: number;
  /** Ajanin bildirdigi alt protokol; `ws-ok` gelene kadar bos string. */
  readonly protocol: string;
  /** Bir mesaj yollar; parcalama ve backpressure iceride. */
  send(binary: boolean, data: Uint8Array): Promise<void>;
  /** Oturumu kapatir ve ajana `ws-close` yollar. */
  close(code?: number, reason?: string): void;
}

/** ws oturumunun olaylari. */
export interface SocketHandlers {
  onOpen?: (proto: string) => void;
  onMessage?: (binary: boolean, data: Uint8Array) => void;
  /** Her oturum icin EN FAZLA bir kez cagrilir. */
  onClose?: (code: number, reason: string) => void;
}

/** Bekleyen bir ws oturumunun durumu. */
interface SocketState {
  readonly id: number;
  readonly handlers: SocketHandlers;
  protocol: string;
  opened: boolean;
  closed: boolean;
  /** Beklenen sonraki seq (gelen yon). */
  recvSeq: number;
  /** Bir sonraki seq (giden yon). */
  sendSeq: number;
  /** Birikmekte olan mesajin parcalari. */
  partial: Uint8Array[];
  partialBytes: number;
  partialBinary: boolean;
  havePartial: boolean;
  /**
   * Gonderim zinciri: iki `send` cagrisi ic ice girmemeli, yoksa seq sirasi
   * bozulur (gonderim backpressure icin await ediyor).
   */
  sendChain: Promise<void>;
}

export class Tunnel {
  private readonly ctrl: RTCDataChannel;
  private readonly data: RTCDataChannel;
  private readonly pending = new Map<number, Pending>();
  private readonly sockets = new Map<number, SocketState>();
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

  /**
   * Tunel uzerinde bir WebSocket oturumu acar (Faz 6).
   *
   * Donus ANINDA kullanilabilir: `ws-ok` beklenmeden gonderilen mesajlar
   * gecerlidir, cunku `ctrl` ile `data` arasinda siralama yok ve ajan
   * `ws-open`'i gorene kadar cerceveleri tamponlar (protocol/README.md).
   */
  openSocket(path: string, protos: readonly string[], handlers: SocketHandlers): TunnelSocket {
    if (!this.isOpen) throw new TunnelError("tunel acik degil");

    const id = this.takeId();
    const state: SocketState = {
      id,
      handlers,
      protocol: "",
      opened: false,
      closed: false,
      recvSeq: 0,
      sendSeq: 0,
      partial: [],
      partialBytes: 0,
      partialBinary: false,
      havePartial: false,
      sendChain: Promise.resolve(),
    };
    this.sockets.set(id, state);

    // Kayit kurulduktan SONRA `ws-open`: cerceveler hemen gelse bile
    // dusecek bir yer var.
    this.sendCtrl({ t: "ws-open", id, path, protos: [...protos] });

    return {
      id,
      get protocol() {
        return state.protocol;
      },
      send: (binary, data) => this.sendSocketMessage(state, binary, data),
      close: (code = 1000, reason = "") => {
        this.closeSocket(state, code, reason, true);
      },
    };
  }

  /** Acik ws oturumu sayisi (durum ekrani icin). */
  get socketCount(): number {
    return this.sockets.size;
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

  /**
   * ws mesajini parcalara bolerek yollar.
   *
   * Gonderimler zincirleniyor: iki cagri ic ice girerse seq sirasi bozulur
   * ve ajan oturumu `bad-seq` ile kapatir.
   */
  private sendSocketMessage(state: SocketState, binary: boolean, data: Uint8Array): Promise<void> {
    const run = state.sendChain.then(async () => {
      if (state.closed) throw new TunnelError("ws oturumu kapali");
      if (this.data.readyState !== "open") throw new TunnelError("data kanali kapandi");

      for (let off = 0; ; off += MAX_WS_PAYLOAD) {
        const end = Math.min(off + MAX_WS_PAYLOAD, data.byteLength);
        const fin = end >= data.byteLength;

        await this.waitWritable();
        if (state.closed) throw new TunnelError("ws oturumu kapali");
        if (this.data.readyState !== "open") throw new TunnelError("data kanali kapandi");

        const payload = encodeWsPayload(binary, fin, data.subarray(off, end));
        this.data.send(encodeFrame(state.id, state.sendSeq, payload));
        state.sendSeq += 1;
        this.bytesOut += payload.byteLength;

        if (fin) return;
      }
    });
    // Zincir kirilmasin: bir hata sonraki gonderimleri engellemez (oturum
    // zaten kapaniyor).
    state.sendChain = run.catch(() => {});
    return run;
  }

  /**
   * ws oturumunu kapatir.
   *
   * `notify` true ise ajana `ws-close` yollanir (yerel kapanis); ajandan
   * `ws-close` geldiginde false olur, aksi halde kapanis ping-pong'u olurdu.
   */
  private closeSocket(state: SocketState, code: number, reason: string, notify: boolean): void {
    if (state.closed) return;
    state.closed = true;
    this.sockets.delete(state.id);

    if (notify && this.ctrl.readyState === "open" && !this.closed) {
      try {
        this.sendCtrl({ t: "ws-close", id: state.id, code, reason });
      } catch {
        // Kanal kapaniyor: haber vermenin bir anlami kalmadi.
      }
    }
    state.handlers.onClose?.(code, reason);
  }

  /** Gelen ws cercevesini isler. */
  private onSocketFrame(state: SocketState, seq: number, payload: Uint8Array<ArrayBuffer>): void {
    // Kanal ordered: beklenmeyen seq protokol hatasi.
    if (seq !== state.recvSeq) {
      this.closeSocket(state, 1002, "bad-seq", true);
      return;
    }
    state.recvSeq += 1;

    let chunk: ReturnType<typeof decodeWsPayload>;
    try {
      chunk = decodeWsPayload(payload);
    } catch {
      this.closeSocket(state, 1002, "ws-bad-frame", true);
      return;
    }

    if (state.havePartial && chunk.binary !== state.partialBinary) {
      this.closeSocket(state, 1002, "ws-mixed-type", true);
      return;
    }
    state.partialBinary = chunk.binary;
    state.havePartial = true;
    if (chunk.payload.byteLength > 0) {
      state.partial.push(chunk.payload);
      state.partialBytes += chunk.payload.byteLength;
    }
    this.bytesIn += payload.byteLength;

    if (!chunk.fin) return;

    const msg = concatChunks(state.partial, state.partialBytes);
    state.partial = [];
    state.partialBytes = 0;
    state.havePartial = false;
    state.handlers.onMessage?.(chunk.binary, msg);
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
      case "ws-ok": {
        const state = this.sockets.get(msg.id);
        if (!state || state.closed) break;
        state.protocol = msg.proto;
        state.opened = true;
        state.handlers.onOpen?.(msg.proto);
        break;
      }
      case "ws-close": {
        const state = this.sockets.get(msg.id);
        if (!state) break;
        // Haber ajandan geldi: geri yollamiyoruz.
        this.closeSocket(state, msg.code, msg.reason, false);
        break;
      }
      case "req":
      case "ws-open":
        console.warn(`[peerpages] ajandan ${msg.t} geldi, protokol ihlali`);
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

    const socket = this.sockets.get(frame.id);
    if (socket) {
      // ws yolu: cerceve govdesi [1B bayrak][parca] (protocol/README.md).
      this.onSocketFrame(socket, frame.seq, frame.payload);
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
    // Acik ws oturumlari da dusmeli. Kod 1006: tarayicinin anormal kapanis
    // icin urettigi kodun aynisi, cunku kapanis el sikismasi yapilmadi.
    for (const state of [...this.sockets.values()]) {
      this.closeSocket(state, 1006, err.message, false);
    }
    const waiters = this.pongWaiters;
    this.pongWaiters = [];
    for (const w of waiters) w();
  }

  private takeId(): number {
    // uint32 sinirinda don; kullanimda olan id'yi atla. HTTP istekleri ve ws
    // oturumlari AYNI id uzayini paylasir (protocol/README.md), ikisi de
    // kontrol edilmeli.
    for (let i = 0; i <= MAX_ID; i++) {
      const id = this.nextId;
      this.nextId = this.nextId >= MAX_ID ? 1 : this.nextId + 1;
      if (!this.pending.has(id) && !this.sockets.has(id)) return id;
    }
    throw new TunnelError("bos istek id'si kalmadi");
  }
}

/** Parcalari tek bir Uint8Array'e birlestirir. */
function concatChunks(chunks: readonly Uint8Array[], total: number): Uint8Array {
  if (chunks.length === 1 && chunks[0] !== undefined && chunks[0].byteLength === total) {
    return chunks[0];
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.byteLength;
  }
  return out;
}
