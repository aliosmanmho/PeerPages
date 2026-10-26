import { beforeEach, describe, expect, it } from "vitest";

import { canonicalCtrl, parseCtrl } from "./protocol/ctrl";
import { encodeFrame, MAX_PAYLOAD } from "./protocol/framing";
import { Tunnel } from "./tunnel";

/**
 * Sahte RTCDataChannel: tarayici olmadan tunel mantigini test eder.
 *
 * Ozellikle kanallar arasi siralama eksikligini (ctrl ve data arasinda sira
 * garantisi yok) burada SERBESTCE taklit edebiliyoruz: `end`'i cercevelerden
 * once ya da sonra yollamak tek satirlik fark.
 */
class FakeChannel extends EventTarget {
  readyState: RTCDataChannelState = "open";
  bufferedAmount = 0;
  bufferedAmountLowThreshold = 0;
  binaryType = "arraybuffer";
  readonly label: string;
  readonly sent: Array<string | Uint8Array> = [];

  constructor(label: string) {
    super();
    this.label = label;
  }

  send(data: string | Uint8Array): void {
    if (this.readyState !== "open") throw new Error("kanal kapali");
    this.sent.push(data);
  }

  /** Karsi taraftan mesaj gelmis gibi yapar. */
  deliver(data: string | ArrayBuffer): void {
    this.dispatchEvent(new MessageEvent("message", { data }));
  }

  close(): void {
    this.readyState = "closed";
    this.dispatchEvent(new Event("close"));
  }

  /** Gonderilen ctrl mesajlarini ayristirip dondurur. */
  ctrlMessages() {
    return this.sent
      .filter((x): x is string => typeof x === "string")
      .map((text) => parseCtrl(text));
  }
}

function asChannel(c: FakeChannel): RTCDataChannel {
  return c as unknown as RTCDataChannel;
}

function frameOf(id: number, seq: number, payload: Uint8Array): ArrayBuffer {
  const f = encodeFrame(id, seq, payload);
  return f.buffer;
}

async function readAll(stream: ReadableStream<Uint8Array>): Promise<Uint8Array> {
  const chunks: Uint8Array[] = [];
  let total = 0;
  const reader = stream.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (value) {
      chunks.push(value);
      total += value.byteLength;
    }
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.byteLength;
  }
  return out;
}

const enc = new TextEncoder();

describe("Tunnel", () => {
  let ctrl: FakeChannel;
  let data: FakeChannel;
  let tunnel: Tunnel;

  beforeEach(() => {
    ctrl = new FakeChannel("ctrl");
    data = new FakeChannel("data");
    tunnel = new Tunnel(asChannel(ctrl), asChannel(data));
  });

  it("acik kanallarla acik sayilir ve backpressure esigini kurar", () => {
    expect(tunnel.isOpen).toBe(true);
    expect(data.bufferedAmountLowThreshold).toBe(256 << 10);
    expect(data.binaryType).toBe("arraybuffer");
  });

  it("req mesajini kanonik bicimde yollar", async () => {
    const promise = tunnel.request({
      method: "get",
      path: "/api/items",
      headers: { accept: "application/json" },
    });

    const msgs = ctrl.ctrlMessages();
    expect(msgs).toHaveLength(1);
    expect(msgs[0]).toEqual({
      t: "req",
      id: 1,
      method: "GET", // buyuk harfe cevrilir
      path: "/api/items",
      headers: { accept: "application/json" },
      hasBody: false,
    });

    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 0, len: 0 }));

    const res = await promise;
    expect(res.status).toBe(200);
    expect(await readAll(res.body)).toEqual(new Uint8Array(0));
  });

  it("cevap govdesini birlestirir", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });

    ctrl.deliver(
      canonicalCtrl({ t: "res", id: 1, status: 200, headers: { "content-type": "text/html" } }),
    );
    data.deliver(frameOf(1, 0, enc.encode("mer")));
    data.deliver(frameOf(1, 1, enc.encode("haba")));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 2, len: 7 }));

    const res = await promise;
    expect(res.headers["content-type"]).toBe("text/html");
    expect(new TextDecoder().decode(await readAll(res.body))).toBe("merhaba");
  });

  /**
   * KRITIK: ctrl ve data ayri SCTP stream'leri. `end` govde cercevelerinden
   * ONCE gelebilir; akis sayaclar tamamlanmadan kapanmamali.
   */
  it("end cercevelerden once gelirse akisi erken kapatmaz", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });

    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    // end ONCE
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 2, len: 7 }));
    // cerceveler SONRA
    data.deliver(frameOf(1, 0, enc.encode("mer")));
    data.deliver(frameOf(1, 1, enc.encode("haba")));

    const res = await promise;
    expect(new TextDecoder().decode(await readAll(res.body))).toBe("merhaba");
  });

  it("res cercevelerden sonra gelse de govde kaybolmaz", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });

    // Cerceveler res'ten ONCE.
    data.deliver(frameOf(1, 0, enc.encode("once")));
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 1, len: 4 }));

    const res = await promise;
    expect(new TextDecoder().decode(await readAll(res.body))).toBe("once");
  });

  it("beklenmeyen seq gelirse abort yollar", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));

    const res = await promise;
    // seq 0 atlanip 3 geliyor.
    data.deliver(frameOf(1, 3, enc.encode("bozuk")));

    await expect(readAll(res.body)).rejects.toThrow();
    const aborts = ctrl.ctrlMessages().filter((m) => m.t === "abort");
    expect(aborts).toHaveLength(1);
    expect(aborts[0]).toMatchObject({ t: "abort", id: 1, reason: "bad-seq" });
  });

  it("bildirilenden fazla govde gelirse abort yollar", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    const res = await promise;

    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 1, len: 3 }));
    data.deliver(frameOf(1, 0, enc.encode("cok-uzun")));

    await expect(readAll(res.body)).rejects.toThrow();
    const aborts = ctrl.ctrlMessages().filter((m) => m.t === "abort");
    expect(aborts[0]).toMatchObject({ reason: "body-length-mismatch" });
  });

  it("ajan abort yollarsa istek hata verir", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    ctrl.deliver(canonicalCtrl({ t: "abort", id: 1, reason: "upstream-read-error" }));
    await expect(promise).rejects.toThrow(/upstream-read-error/);
  });

  it("istek govdesini cercevelere boler ve end sayaclarini dogru yollar", async () => {
    const body = new Uint8Array(MAX_PAYLOAD + 10).fill(7);
    const promise = tunnel.request({
      method: "POST",
      path: "/api",
      headers: { "content-type": "application/octet-stream" },
      body,
    });

    // Govde arka planda akitiliyor; mikro gorevlerin bitmesini bekle.
    await new Promise((r) => setTimeout(r, 10));

    const frames = data.sent.filter((x): x is Uint8Array => typeof x !== "string");
    expect(frames).toHaveLength(2);

    const msgs = ctrl.ctrlMessages();
    expect(msgs[0]).toMatchObject({ t: "req", hasBody: true });
    const end = msgs.find((m) => m.t === "end");
    expect(end).toEqual({ t: "end", id: 1, frames: 2, len: MAX_PAYLOAD + 10 });

    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 201, headers: {} }));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 0, len: 0 }));
    expect((await promise).status).toBe(201);
  });

  it("ping/pong gidis donus suresini olcer", async () => {
    const promise = tunnel.ping(1000);
    expect(ctrl.ctrlMessages().some((m) => m.t === "ping")).toBe(true);
    ctrl.deliver(canonicalCtrl({ t: "pong" }));
    expect(await promise).toBeGreaterThanOrEqual(0);
  });

  it("ajandan gelen ping'e pong doner", () => {
    ctrl.deliver(canonicalCtrl({ t: "ping" }));
    expect(ctrl.ctrlMessages().some((m) => m.t === "pong")).toBe(true);
  });

  it("hello mesajini saklar", () => {
    ctrl.deliver(canonicalCtrl({ t: "hello", v: 1, targets: ["default", "admin"], auth: "none" }));
    expect(tunnel.hello?.targets).toEqual(["default", "admin"]);
  });

  it("bozuk ctrl mesaji tuneli dusurmez", async () => {
    ctrl.deliver("bu json degil");
    ctrl.deliver('{"t":"zipzip"}');
    expect(tunnel.isOpen).toBe(true);

    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 204, headers: {} }));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 0, len: 0 }));
    expect((await promise).status).toBe(204);
  });

  it("bozuk cerceve tuneli dusurmez", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    // 8 bayttan kisa: short-frame.
    data.deliver(new Uint8Array([1, 2, 3]).buffer);

    data.deliver(frameOf(1, 0, enc.encode("iyi")));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 1, len: 3 }));

    const res = await promise;
    expect(new TextDecoder().decode(await readAll(res.body))).toBe("iyi");
  });

  it("kanal kapaninca bekleyen istekler hata verir", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    data.close();
    await expect(promise).rejects.toThrow(/tunel kapandi/);
    expect(tunnel.isOpen).toBe(false);
  });

  it("kapali tunelde istek yapilmaz", async () => {
    tunnel.close();
    await expect(tunnel.request({ method: "GET", path: "/", headers: {} })).rejects.toThrow(
      /acik degil/,
    );
  });

  it("AbortSignal istegi iptal eder ve ajana bildirir", async () => {
    const ac = new AbortController();
    const promise = tunnel.request({
      method: "GET",
      path: "/",
      headers: {},
      signal: ac.signal,
    });
    ac.abort();

    await expect(promise).rejects.toThrow(/iptal/);
    const aborts = ctrl.ctrlMessages().filter((m) => m.t === "abort");
    expect(aborts[0]).toMatchObject({ reason: "client-cancel" });
  });

  it("eszamanli istekler ayri id alir ve karismaz", async () => {
    const p1 = tunnel.request({ method: "GET", path: "/bir", headers: {} });
    const p2 = tunnel.request({ method: "GET", path: "/iki", headers: {} });

    const reqs = ctrl.ctrlMessages().filter((m) => m.t === "req");
    expect(reqs.map((r) => (r as { id: number }).id)).toEqual([1, 2]);

    // Cevaplar ters sirada ve cerceveler ic ice gelsin.
    ctrl.deliver(canonicalCtrl({ t: "res", id: 2, status: 200, headers: {} }));
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    data.deliver(frameOf(2, 0, enc.encode("IKI")));
    data.deliver(frameOf(1, 0, enc.encode("BIR")));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 1, len: 3 }));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 2, frames: 1, len: 3 }));

    const [r1, r2] = await Promise.all([p1, p2]);
    expect(new TextDecoder().decode(await readAll(r1.body))).toBe("BIR");
    expect(new TextDecoder().decode(await readAll(r2.body))).toBe("IKI");
  });

  it("istatistikleri sayar", async () => {
    const promise = tunnel.request({ method: "GET", path: "/", headers: {} });
    ctrl.deliver(canonicalCtrl({ t: "res", id: 1, status: 200, headers: {} }));
    data.deliver(frameOf(1, 0, new Uint8Array(100)));
    ctrl.deliver(canonicalCtrl({ t: "end", id: 1, frames: 1, len: 100 }));
    await promise;

    expect(tunnel.stats.requests).toBe(1);
    expect(tunnel.stats.bytesIn).toBe(100);
    expect(tunnel.stats.inflight).toBe(0);
  });
});
