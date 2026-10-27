import { beforeEach, describe, expect, it } from "vitest";

import { type CtrlMsg, canonicalCtrl, parseCtrl } from "./protocol/ctrl";
import { decodeFrame, encodeFrame } from "./protocol/framing";
import { decodeWsPayload, encodeWsPayload, MAX_WS_PAYLOAD } from "./protocol/wsframe";
import { Tunnel } from "./tunnel";

/**
 * Tunel uzerindeki WebSocket oturumlari (Faz 6).
 *
 * Sahte kanallarla: tarayici gerekmiyor ve kanallar arasi siralama
 * eksikligini serbestce taklit edebiliyoruz.
 */

class FakeChannel extends EventTarget {
  readyState: RTCDataChannelState = "open";
  bufferedAmount = 0;
  bufferedAmountLowThreshold = 0;
  binaryType = "arraybuffer";
  readonly sent: Array<string | Uint8Array> = [];

  send(data: string | Uint8Array): void {
    if (this.readyState !== "open") throw new Error("kanal kapali");
    this.sent.push(data);
  }

  deliver(data: string | ArrayBuffer): void {
    this.dispatchEvent(new MessageEvent("message", { data }));
  }

  close(): void {
    this.readyState = "closed";
    this.dispatchEvent(new Event("close"));
  }

  ctrlMessages(): CtrlMsg[] {
    return this.sent.filter((x): x is string => typeof x === "string").map((t) => parseCtrl(t));
  }

  /** Gonderilen ws cercevelerini cozer. */
  wsFrames() {
    return this.sent
      .filter((x): x is Uint8Array => typeof x !== "string")
      .map((raw) => {
        const f = decodeFrame(raw);
        return { id: f.id, seq: f.seq, chunk: decodeWsPayload(f.payload) };
      });
  }
}

function asChannel(c: FakeChannel): RTCDataChannel {
  return c as unknown as RTCDataChannel;
}

/** Ajandan gelmis gibi bir ws cercevesi uretir. */
function wsFrame(id: number, seq: number, binary: boolean, fin: boolean, data: Uint8Array) {
  return encodeFrame(id, seq, encodeWsPayload(binary, fin, data)).buffer;
}

interface Recorder {
  opened: string[];
  messages: Array<{ binary: boolean; text: string; bytes: number }>;
  closes: Array<{ code: number; reason: string }>;
}

function recorder(): Recorder {
  return { opened: [], messages: [], closes: [] };
}

const enc = new TextEncoder();
const dec = new TextDecoder();

describe("Tunnel.openSocket", () => {
  let ctrl: FakeChannel;
  let data: FakeChannel;
  let tunnel: Tunnel;
  let rec: Recorder;

  beforeEach(() => {
    ctrl = new FakeChannel();
    data = new FakeChannel();
    tunnel = new Tunnel(asChannel(ctrl), asChannel(data));
    rec = recorder();
  });

  function open(path = "/socket", protos: string[] = []) {
    return tunnel.openSocket(path, protos, {
      onOpen: (proto) => rec.opened.push(proto),
      onMessage: (binary, d) =>
        rec.messages.push({ binary, text: dec.decode(d), bytes: d.byteLength }),
      onClose: (code, reason) => rec.closes.push({ code, reason }),
    });
  }

  it("ws-open mesajini kanonik bicimde yollar", () => {
    open("/socket?x=1", ["vite-hmr"]);
    const msgs = ctrl.ctrlMessages();
    expect(msgs).toHaveLength(1);
    expect(msgs[0]).toEqual({
      t: "ws-open",
      id: 1,
      path: "/socket?x=1",
      protos: ["vite-hmr"],
    });
  });

  it("ws-ok alt protokolu bildirir", () => {
    const sock = open();
    expect(sock.protocol).toBe("");
    ctrl.deliver(canonicalCtrl({ t: "ws-ok", id: sock.id, proto: "json" }));
    expect(rec.opened).toEqual(["json"]);
    expect(sock.protocol).toBe("json");
  });

  it("gelen mesaji FIN bayraginda tamamlar", () => {
    const sock = open();
    ctrl.deliver(canonicalCtrl({ t: "ws-ok", id: sock.id, proto: "" }));

    // Iki parcali tek mesaj: ilk parcada FIN yok.
    data.deliver(wsFrame(sock.id, 0, false, false, enc.encode("mer")));
    expect(rec.messages).toHaveLength(0);
    data.deliver(wsFrame(sock.id, 1, false, true, enc.encode("haba")));

    expect(rec.messages).toEqual([{ binary: false, text: "merhaba", bytes: 7 }]);
  });

  it("bos mesaji da teslim eder", () => {
    const sock = open();
    data.deliver(wsFrame(sock.id, 0, false, true, new Uint8Array()));
    expect(rec.messages).toEqual([{ binary: false, text: "", bytes: 0 }]);
  });

  it("ikili mesaji isaretler", () => {
    const sock = open();
    data.deliver(wsFrame(sock.id, 0, true, true, new Uint8Array([1, 2, 3])));
    expect(rec.messages[0]?.binary).toBe(true);
    expect(rec.messages[0]?.bytes).toBe(3);
  });

  it("gonderilen mesaji parcalara boler ve seq artirir", async () => {
    const sock = open();
    const big = new Uint8Array(MAX_WS_PAYLOAD + 10);
    big.fill(7);
    await sock.send(true, big);

    const frames = data.wsFrames();
    expect(frames).toHaveLength(2);
    expect(frames[0]).toMatchObject({ id: sock.id, seq: 0 });
    expect(frames[0]?.chunk.fin).toBe(false);
    expect(frames[0]?.chunk.binary).toBe(true);
    expect(frames[0]?.chunk.payload.byteLength).toBe(MAX_WS_PAYLOAD);
    expect(frames[1]).toMatchObject({ id: sock.id, seq: 1 });
    expect(frames[1]?.chunk.fin).toBe(true);
    expect(frames[1]?.chunk.payload.byteLength).toBe(10);
  });

  it("ard arda gonderimlerde seq sirasi bozulmaz", async () => {
    const sock = open();
    // Beklemeden ust uste: zincirleme olmasa seq sirasi karisirdi.
    const a = sock.send(false, enc.encode("bir"));
    const b = sock.send(false, enc.encode("iki"));
    await Promise.all([a, b]);

    const frames = data.wsFrames();
    expect(frames.map((f) => f.seq)).toEqual([0, 1]);
    expect(dec.decode(frames[0]?.chunk.payload)).toBe("bir");
    expect(dec.decode(frames[1]?.chunk.payload)).toBe("iki");
  });

  it("yerel kapanis ws-close yollar", () => {
    const sock = open();
    sock.close(4001, "bitti");

    const last = ctrl.ctrlMessages().at(-1);
    expect(last).toEqual({ t: "ws-close", id: sock.id, code: 4001, reason: "bitti" });
    expect(rec.closes).toEqual([{ code: 4001, reason: "bitti" }]);
  });

  it("ajandan gelen ws-close geri yollanmaz", () => {
    const sock = open();
    const before = ctrl.ctrlMessages().length;
    ctrl.deliver(canonicalCtrl({ t: "ws-close", id: sock.id, code: 1000, reason: "" }));

    expect(rec.closes).toEqual([{ code: 1000, reason: "" }]);
    // Kapanis ping-pong'u olmamali.
    expect(ctrl.ctrlMessages()).toHaveLength(before);
  });

  it("onClose yalnizca bir kez cagrilir", () => {
    const sock = open();
    sock.close(1000, "");
    sock.close(1000, "");
    ctrl.deliver(canonicalCtrl({ t: "ws-close", id: sock.id, code: 1001, reason: "" }));
    expect(rec.closes).toHaveLength(1);
  });

  it("bozuk seq oturumu 1002 ile kapatir", () => {
    const sock = open();
    // seq 0 bekleniyor, 1 geliyor.
    data.deliver(wsFrame(sock.id, 1, false, true, enc.encode("x")));

    expect(rec.closes).toEqual([{ code: 1002, reason: "bad-seq" }]);
    expect(ctrl.ctrlMessages().at(-1)).toMatchObject({ t: "ws-close", code: 1002 });
  });

  it("tanimsiz bayrak oturumu 1002 ile kapatir", () => {
    const sock = open();
    data.deliver(encodeFrame(sock.id, 0, new Uint8Array([0xff, 1])).buffer);
    expect(rec.closes[0]?.code).toBe(1002);
  });

  it("bos payload oturumu 1002 ile kapatir", () => {
    const sock = open();
    data.deliver(encodeFrame(sock.id, 0, new Uint8Array()).buffer);
    expect(rec.closes[0]?.code).toBe(1002);
  });

  it("mesajin parcalari farkli tipte olamaz", () => {
    const sock = open();
    data.deliver(wsFrame(sock.id, 0, false, false, enc.encode("a")));
    data.deliver(wsFrame(sock.id, 1, true, true, new Uint8Array([2])));
    expect(rec.closes).toEqual([{ code: 1002, reason: "ws-mixed-type" }]);
  });

  it("tunel kapaninca oturumlar 1006 ile duser", () => {
    open();
    open();
    expect(tunnel.socketCount).toBe(2);

    ctrl.close();
    expect(rec.closes.map((c) => c.code)).toEqual([1006, 1006]);
    expect(tunnel.socketCount).toBe(0);
  });

  it("id uzayi HTTP istekleriyle paylasilir", () => {
    const sock = open();
    void tunnel.request({ method: "GET", path: "/", headers: {} });
    const ids = ctrl
      .ctrlMessages()
      .map((m) => ("id" in m ? m.id : -1))
      .filter((id) => id >= 0);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toContain(sock.id);
  });

  it("kapali oturuma gonderim hata verir", async () => {
    const sock = open();
    sock.close();
    await expect(sock.send(false, enc.encode("x"))).rejects.toThrow();
  });

  it("acik oturum sayisini bildirir", () => {
    const a = open();
    open();
    expect(tunnel.socketCount).toBe(2);
    a.close();
    expect(tunnel.socketCount).toBe(1);
  });
});
