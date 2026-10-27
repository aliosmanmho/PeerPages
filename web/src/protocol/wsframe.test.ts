import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { encodeFrame, MAX_PAYLOAD } from "./framing";
import { fromHex, type TestVectors, toHex } from "./vectors";
import {
  decodeWsPayload,
  encodeWsPayload,
  MAX_WS_PAYLOAD,
  WS_HEADER_BYTES,
  WsFrameError,
} from "./wsframe";

const vectorsPath = fileURLToPath(new URL("../../../protocol/testvectors.json", import.meta.url));
const V = JSON.parse(readFileSync(vectorsPath, "utf8")) as TestVectors;

describe("ws cerceve alt bicimi — sabitler vektorle ayni", () => {
  it("baslik ve sinir", () => {
    expect(V.wsFrames.limits.headerBytes).toBe(WS_HEADER_BYTES);
    expect(V.wsFrames.limits.maxPayload).toBe(MAX_WS_PAYLOAD);
  });
});

describe("encodeWsPayload — gecerli vektorler", () => {
  for (const v of V.wsFrames.valid) {
    it(v.name, () => {
      // Cikti Go tarafiyla bayt bayt ayni olmali.
      expect(toHex(encodeWsPayload(v.binary, v.fin, fromHex(v.payloadHex)))).toBe(v.outHex);
    });
  }
});

describe("decodeWsPayload — gecerli vektorler", () => {
  for (const v of V.wsFrames.valid) {
    it(v.name, () => {
      const chunk = decodeWsPayload(fromHex(v.outHex) as Uint8Array<ArrayBuffer>);
      expect(chunk.binary).toBe(v.binary);
      expect(chunk.fin).toBe(v.fin);
      expect(toHex(chunk.payload)).toBe(v.payloadHex);
    });
  }
});

describe("decodeWsPayload — reddedilmesi gerekenler", () => {
  for (const v of V.wsFrames.decodeInvalid) {
    it(`${v.name} → ${v.error}`, () => {
      try {
        const got = decodeWsPayload(fromHex(v.hex) as Uint8Array<ArrayBuffer>);
        throw new Error(`hata beklenirdi, donen: ${JSON.stringify(got)}`);
      } catch (err) {
        expect(err).toBeInstanceOf(WsFrameError);
        expect((err as WsFrameError).code).toBe(v.error);
      }
    });
  }
});

describe("ws parcasi dis cerceveye sigiyor", () => {
  it("sinirdaki parca tam MAX_PAYLOAD uretir", () => {
    const out = encodeWsPayload(false, true, new Uint8Array(MAX_WS_PAYLOAD));
    expect(out.byteLength).toBe(MAX_PAYLOAD);
    // Dis cerceve reddetmemeli.
    expect(() => encodeFrame(1, 0, out)).not.toThrow();
  });

  it("sinir asimi ws-payload-too-large", () => {
    expect(() => encodeWsPayload(false, true, new Uint8Array(MAX_WS_PAYLOAD + 1))).toThrow(
      WsFrameError,
    );
  });
});

describe("decodeWsPayload tamponu paylasir", () => {
  // Bilincli: decodeFrame zaten kopya uretiyor, ikinci kopya gereksiz.
  it("donen payload bir gorunum", () => {
    const buf = new Uint8Array([0x02, 0x61, 0x62]) as Uint8Array<ArrayBuffer>;
    const chunk = decodeWsPayload(buf);
    buf[1] = 0x7a;
    expect(chunk.payload[0]).toBe(0x7a);
  });
});
