import { describe, expect, it } from "vitest";

import { INJECT_SCAN_LIMIT, injectScriptStream, planInjection } from "./inject";

const enc = new TextEncoder();
const dec = new TextDecoder();

/** Govdeyi verilen parcalara bolup enjektorden gecirir. */
async function run(chunks: string[], src = "/PeerPages/ws-shim.js"): Promise<string> {
  const stream = new ReadableStream<Uint8Array<ArrayBuffer>>({
    start(controller) {
      for (const c of chunks) {
        controller.enqueue(enc.encode(c) as Uint8Array<ArrayBuffer>);
      }
      controller.close();
    },
  });

  const out: Uint8Array[] = [];
  const reader = stream.pipeThrough(injectScriptStream(src)).getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (value) out.push(value);
  }
  let total = 0;
  for (const c of out) total += c.byteLength;
  const joined = new Uint8Array(total);
  let off = 0;
  for (const c of out) {
    joined.set(c, off);
    off += c.byteLength;
  }
  return dec.decode(joined);
}

const TAG = '<script src="/PeerPages/ws-shim.js"></script>';

describe("planInjection", () => {
  it("head etiketinin kapanisindan sonrasini secer", () => {
    expect(planInjection("<html><head>", false)).toEqual({ offset: 12, decided: true });
  });

  it("head nitelikliyse de kapanisi bulur", () => {
    const html = '<html><head lang="tr" data-x="1">';
    expect(planInjection(html, false)).toEqual({ offset: html.length, decided: true });
  });

  it("head acilmis ama kapanmamissa bekler", () => {
    expect(planInjection("<html><head lang=", false).decided).toBe(false);
  });

  it("head yoksa body'den once koyar", () => {
    expect(planInjection("<html><body>x", false)).toEqual({ offset: 6, decided: true });
  });

  it("ikisi de yoksa ve akis bitmediyse bekler", () => {
    expect(planInjection("<!doctype html>\n", false).decided).toBe(false);
  });

  it("akis bittiyse basa koyar", () => {
    expect(planInjection("merhaba", true)).toEqual({ offset: 0, decided: true });
  });

  it("tarama siniri dolunca basa koyar", () => {
    const long = "x".repeat(INJECT_SCAN_LIMIT);
    expect(planInjection(long, false)).toEqual({ offset: 0, decided: true });
  });

  it("buyuk/kucuk harf duyarsiz", () => {
    expect(planInjection("<HTML><HEAD>", false)).toEqual({ offset: 12, decided: true });
  });
});

describe("injectScriptStream", () => {
  it("script'i head'den hemen sonra sokar", async () => {
    const out = await run(["<html><head><title>a</title></head><body>b</body></html>"]);
    expect(out).toBe(`<html><head>${TAG}<title>a</title></head><body>b</body></html>`);
  });

  // Asil risk bu: etiket iki cerceveye bolunmus olabilir.
  it("head etiketi cerceveye bolunmusse de calisir", async () => {
    const out = await run(["<html><he", "ad><title>a</title>", "</head></html>"]);
    expect(out).toBe(`<html><head>${TAG}<title>a</title></head></html>`);
  });

  it("head yoksa body'den once sokar", async () => {
    const out = await run(["<html><body>selam</body></html>"]);
    expect(out).toBe(`<html>${TAG}<body>selam</body></html>`);
  });

  it("ne head ne body varsa basa sokar", async () => {
    const out = await run(["<div>parca</div>"]);
    expect(out).toBe(`${TAG}<div>parca</div>`);
  });

  it("govdenin geri kalanina dokunmaz", async () => {
    const tail = "x".repeat(100_000);
    const out = await run(["<html><head></head><body>", tail, "</body></html>"]);
    expect(out.endsWith(`${tail}</body></html>`)).toBe(true);
    expect(out.split(TAG)).toHaveLength(2);
  });

  it("yalnizca BIR kez enjekte eder", async () => {
    const out = await run(["<head></head>", "<head></head>"]);
    expect(out.split(TAG)).toHaveLength(2);
  });

  // ASCII arama yapiyoruz ama govde UTF-8: bayt indeksi metin indeksinden
  // farkli, kaydirma olmamali.
  it("cok baytli karakterler indeksi kaydirmaz", async () => {
    const out = await run(["<html><head><title>çığırtkan ölçüm</title></head>"]);
    expect(out).toBe("<html><head>" + TAG + "<title>çığırtkan ölçüm</title></head>");
  });

  it("bos govdeyi bozmaz", async () => {
    expect(await run([])).toBe(TAG);
  });
});
