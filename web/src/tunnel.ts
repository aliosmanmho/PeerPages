/**
 * The tunnel on the page side: request multiplexing over ctrl/data.
 *
 * Every `fetch` the Service Worker catches is handed to the page and
 * turned into a `req` message here. The response comes back as a stream
 * (`ReadableStream`); the full body is never awaited.
 *
 * There is NO ordering between the channels (protocol/README.md):
 *  - response frames can arrive before the `res` message, so the request
 *    record is created while sending `req` and a frame always has
 *    somewhere to land;
 *  - `end` can arrive before the frames, so a stream is not closed until
 *    the `frames`/`len` counters from `end` are complete.
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

/** Backpressure thresholds — same as the agent side (protocol/README.md). */
const BUFFERED_HIGH = 1 << 20; // 1 MiB
const BUFFERED_LOW = 256 << 10; // 256 KiB

/** How long we wait for the `res` message. */
const RESPONSE_TIMEOUT_MS = 30_000;

/** Request id, wrapping at the uint32 boundary. */
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
  /** Request body; null when there is none. */
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
 * Queue that turns incoming body chunks into a ReadableStream.
 *
 * `pull` based: chunks are handed over as the consumer reads, so memory
 * stays bounded while the Service Worker streams a response.
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

/** State of a pending request. */
interface Pending {
  readonly id: number;
  readonly body: BodyQueue;
  /** Frames received so far; also the next expected seq. */
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

/** A WebSocket session over the tunnel (phase 6). */
export interface TunnelSocket {
  readonly id: number;
  /** Subprotocol the agent reported; empty string until `ws-ok` arrives. */
  readonly protocol: string;
  /** Sends one message; chunking and backpressure happen inside. */
  send(binary: boolean, data: Uint8Array): Promise<void>;
  /** Closes the session and sends `ws-close` to the agent. */
  close(code?: number, reason?: string): void;
}

/** ws oturumunun olaylari. */
export interface SocketHandlers {
  onOpen?: (proto: string) => void;
  onMessage?: (binary: boolean, data: Uint8Array) => void;
  /** Called AT MOST once per session. */
  onClose?: (code: number, reason: string) => void;
}

/** State of a pending ws session. */
interface SocketState {
  readonly id: number;
  readonly handlers: SocketHandlers;
  protocol: string;
  opened: boolean;
  closed: boolean;
  /** Next expected seq (incoming direction). */
  recvSeq: number;
  /** Next seq to use (outgoing direction). */
  sendSeq: number;
  /** Birikmekte olan mesajin parcalari. */
  partial: Uint8Array[];
  partialBytes: number;
  partialBinary: boolean;
  havePartial: boolean;
  /**
   * Send chain: two `send` calls must not interleave, or the seq order
   * breaks (sending awaits for backpressure).
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

  /** The `hello` message from the agent (target list), once it arrives. */
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
      this.closeAll(new TunnelError("tunnel closed"));
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

  /** Closes the tunnel and fails every pending request. */
  close(): void {
    this.closeAll(new TunnelError("tunnel was closed"));
  }

  /** ctrl kanalinda ping/pong gidis donus suresini olcer (ms). */
  async ping(timeoutMs = 5000): Promise<number> {
    if (!this.isOpen) throw new TunnelError("tunnel is not open");

    const started = performance.now();
    const waiter = new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pongWaiters = this.pongWaiters.filter((w) => w !== onPong);
        reject(new TunnelError("the pong timed out"));
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
   * Makes a request over the tunnel.
   *
   * The promise resolves when `res` arrives; the body streams in.
   */
  async request(init: RequestInit_): Promise<TunnelResponse> {
    if (!this.isOpen) throw new TunnelError("tunnel is not open");
    if (init.signal?.aborted) throw new TunnelError("request was aborted");

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
      this.failPending(id, new TunnelError("response timed out (no res)"));
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

    // The record is created BEFORE `req` goes out: even if response
    // frames arrive immediately, they have somewhere to land.
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
   * Opens a WebSocket session over the tunnel (phase 6).
   *
   * Usable IMMEDIATELY: messages sent before `ws-ok` are valid, because
   * there is no ordering between `ctrl` and `data` and the agent buffers
   * frames until it sees `ws-open` (protocol/README.md).
   */
  openSocket(path: string, protos: readonly string[], handlers: SocketHandlers): TunnelSocket {
    if (!this.isOpen) throw new TunnelError("tunnel is not open");

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

    // Registered BEFORE `ws-open` goes out: even if frames arrive
    // immediately, they have somewhere to land.
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

  /** Number of open ws sessions (for the status panel). */
  get socketCount(): number {
    return this.sockets.size;
  }

  // --- gonderim ---

  private sendCtrl(msg: CtrlMsg): void {
    if (this.ctrl.readyState !== "open") {
      throw new TunnelError(`ctrl channel is not open (${this.ctrl.readyState})`);
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
      // frames/len are REQUIRED: that is how the receiver knows the body
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
   * and the agent closes the session with `bad-seq`.
   */
  private sendSocketMessage(state: SocketState, binary: boolean, data: Uint8Array): Promise<void> {
    const run = state.sendChain.then(async () => {
      if (state.closed) throw new TunnelError("ws session is closed");
      if (this.data.readyState !== "open") throw new TunnelError("data kanali kapandi");

      for (let off = 0; ; off += MAX_WS_PAYLOAD) {
        const end = Math.min(off + MAX_WS_PAYLOAD, data.byteLength);
        const fin = end >= data.byteLength;

        await this.waitWritable();
        if (state.closed) throw new TunnelError("ws session is closed");
        if (this.data.readyState !== "open") throw new TunnelError("data kanali kapandi");

        const payload = encodeWsPayload(binary, fin, data.subarray(off, end));
        this.data.send(encodeFrame(state.id, state.sendSeq, payload));
        state.sendSeq += 1;
        this.bytesOut += payload.byteLength;

        if (fin) return;
      }
    });
    // Keep the chain alive: one failure must not block later sends (the
    // session is closing anyway).
    state.sendChain = run.catch(() => {});
    return run;
  }

  /**
   * ws oturumunu kapatir.
   *
   * With `notify` true we send `ws-close` to the agent (a local close);
   * it is false when the close came FROM the agent, otherwise the two
   */
  private closeSocket(state: SocketState, code: number, reason: string, notify: boolean): void {
    if (state.closed) return;
    state.closed = true;
    this.sockets.delete(state.id);

    if (notify && this.ctrl.readyState === "open" && !this.closed) {
      try {
        this.sendCtrl({ t: "ws-close", id: state.id, code, reason });
      } catch {
        // The channel is closing; telling anyone is pointless now.
      }
    }
    state.handlers.onClose?.(code, reason);
  }

  /** Handles an incoming ws frame. */
  private onSocketFrame(state: SocketState, seq: number, payload: Uint8Array<ArrayBuffer>): void {
    // The channel is ordered: an unexpected seq is a protocol error.
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

  /** Waits until the data channel send buffer drains. */
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
      // The channel may already be closed; end the stream regardless.
    }
    this.failPending(id, new TunnelError(`request aborted: ${reason}`));
  }

  // --- alim ---

  private onCtrl(ev: MessageEvent<string>): void {
    let msg: CtrlMsg;
    try {
      msg = parseCtrl(typeof ev.data === "string" ? ev.data : String(ev.data));
    } catch (err) {
      // The agent should not send a malformed message; skip it without dropping the tunnel.
      console.warn("[peerpages] invalid ctrl message:", err);
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
    // If `end` already arrived and the counters match, close now.
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
    this.failPending(msg.id, new TunnelError(`the agent aborted the stream: ${msg.reason}`));
  }

  private onData(ev: MessageEvent<ArrayBuffer>): void {
    let frame: ReturnType<typeof decodeFrame>;
    try {
      const raw = ev.data;
      const bytes =
        raw instanceof ArrayBuffer ? new Uint8Array(raw) : new Uint8Array(raw as ArrayBuffer);
      frame = decodeFrame(bytes);
    } catch (err) {
      console.warn("[peerpages] invalid data frame:", err);
      return;
    }

    const socket = this.sockets.get(frame.id);
    if (socket) {
      // ws path: the frame body is [1B flags][chunk] (protocol/README.md).
      this.onSocketFrame(socket, frame.seq, frame.payload);
      return;
    }

    const entry = this.pending.get(frame.id);
    if (!entry) {
      // Request already settled or aborted: drop the frame.
      return;
    }

    // The channel is ordered: an unexpected seq is a protocol error.
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
   * Closes the stream once `end` arrived AND the counters are complete.
   *
   * The counter check is required: with no ordering between ctrl and data,
   * `end` can arrive before the frames (protocol/README.md).
   */
  private checkComplete(entry: Pending): void {
    if (!entry.endSeen) return;

    if (entry.frames > entry.expectFrames || entry.bytes > entry.expectLen) {
      this.abortRequest(entry.id, "body-length-mismatch");
      return;
    }
    if (entry.frames < entry.expectFrames || entry.bytes < entry.expectLen) {
      return; // still waiting for frames
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
    // Open ws sessions have to drop too. Code 1006 is what a browser
    // produces for an abnormal close, and no close handshake happened.
    for (const state of [...this.sockets.values()]) {
      this.closeSocket(state, 1006, err.message, false);
    }
    const waiters = this.pongWaiters;
    this.pongWaiters = [];
    for (const w of waiters) w();
  }

  private takeId(): number {
    // Wrap at the uint32 boundary, skipping ids in use. HTTP requests and
    // ws sessions share the SAME id space (protocol/README.md), so both
    // have to be checked.
    for (let i = 0; i <= MAX_ID; i++) {
      const id = this.nextId;
      this.nextId = this.nextId >= MAX_ID ? 1 : this.nextId + 1;
      if (!this.pending.has(id) && !this.sockets.has(id)) return id;
    }
    throw new TunnelError("no free request id left");
  }
}

/** Joins the chunks into a single Uint8Array. */
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
