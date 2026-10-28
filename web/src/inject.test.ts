import { describe, expect, it } from "vitest";

import { INJECT_SCAN_LIMIT, injectScriptStream, planInjection } from "./inject";

const enc = new TextEncoder();
const dec = new TextDecoder();

/** Splits the body into the given chunks and runs it through the injector. */
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
  it("it picks the point right after the head tag closes", () => {
    expect(planInjection("<html><head>", false)).toEqual({ offset: 12, decided: true });
  });

  it("it finds the close even when head has attributes", () => {
    const html = '<html><head lang="tr" data-x="1">';
    expect(planInjection(html, false)).toEqual({ offset: html.length, decided: true });
  });

  it("with head opened but not closed it waits", () => {
    expect(planInjection("<html><head lang=", false).decided).toBe(false);
  });

  it("with no head it puts it before body", () => {
    expect(planInjection("<html><body>x", false)).toEqual({ offset: 6, decided: true });
  });

  it("with neither present and the stream unfinished it waits", () => {
    expect(planInjection("<!doctype html>\n", false).decided).toBe(false);
  });

  it("once the stream has ended it puts it at the start", () => {
    expect(planInjection("merhaba", true)).toEqual({ offset: 0, decided: true });
  });

  it("once the scan limit is reached it puts it at the start", () => {
    const long = "x".repeat(INJECT_SCAN_LIMIT);
    expect(planInjection(long, false)).toEqual({ offset: 0, decided: true });
  });

  it("it is case insensitive", () => {
    expect(planInjection("<HTML><HEAD>", false)).toEqual({ offset: 12, decided: true });
  });
});

describe("injectScriptStream", () => {
  it("it injects the script right after head", async () => {
    const out = await run(["<html><head><title>a</title></head><body>b</body></html>"]);
    expect(out).toBe(`<html><head>${TAG}<title>a</title></head><body>b</body></html>`);
  });

  // This is the real risk: the tag may be split across two frames.
  it("it works even when the head tag is split across frames", async () => {
    const out = await run(["<html><he", "ad><title>a</title>", "</head></html>"]);
    expect(out).toBe(`<html><head>${TAG}<title>a</title></head></html>`);
  });

  it("with no head it injects before body", async () => {
    const out = await run(["<html><body>selam</body></html>"]);
    expect(out).toBe(`<html>${TAG}<body>selam</body></html>`);
  });

  it("with neither head nor body it injects at the start", async () => {
    const out = await run(["<div>chunk</div>"]);
    expect(out).toBe(`${TAG}<div>chunk</div>`);
  });

  it("it does not touch the rest of the body", async () => {
    const tail = "x".repeat(100_000);
    const out = await run(["<html><head></head><body>", tail, "</body></html>"]);
    expect(out.endsWith(`${tail}</body></html>`)).toBe(true);
    expect(out.split(TAG)).toHaveLength(2);
  });

  it("it injects exactly ONCE", async () => {
    const out = await run(["<head></head>", "<head></head>"]);
    expect(out.split(TAG)).toHaveLength(2);
  });

  // The search is ASCII but the body is UTF-8: the byte index differs
  // from the text index, and nothing may shift.
  it("multi-byte characters do not shift the index", async () => {
    const out = await run(["<html><head><title>a loud é✓ß measurement</title></head>"]);
    expect(out).toBe("<html><head>" + TAG + "<title>a loud é✓ß measurement</title></head>");
  });

  it("it does not break an empty body", async () => {
    expect(await run([])).toBe(TAG);
  });
});
