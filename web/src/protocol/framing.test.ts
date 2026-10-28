import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  decodeFrame,
  encodeFrame,
  FramingError,
  frameBody,
  HEADER_BYTES,
  MAX_FRAME,
  MAX_PAYLOAD,
} from "./framing";
import { buildPattern, fromHex, type TestVectors, toHex } from "./vectors";

// The Go side (agent/internal/protocol/framing_test.go) reads the SAME
// file.
const vectorsPath = fileURLToPath(new URL("../../../protocol/testvectors.json", import.meta.url));
const V = JSON.parse(readFileSync(vectorsPath, "utf8")) as TestVectors;

describe("testvectors.json", () => {
  it("it reports the same values as the TS constants", () => {
    expect(V.framing.headerBytes).toBe(HEADER_BYTES);
    expect(V.framing.maxPayload).toBe(MAX_PAYLOAD);
    expect(V.framing.maxFrame).toBe(MAX_FRAME);
  });

  it("it holds the expected number of vectors (so it cannot empty by accident)", () => {
    expect(V.frames.valid.length).toBeGreaterThanOrEqual(6);
    expect(V.frames.generated.length).toBeGreaterThanOrEqual(3);
    expect(V.frames.encodeInvalid.length).toBeGreaterThanOrEqual(2);
    expect(V.frames.decodeInvalid.length).toBeGreaterThanOrEqual(4);
  });
});

describe("encodeFrame — the valid vectors", () => {
  for (const v of V.frames.valid) {
    it(v.name, () => {
      const got = encodeFrame(v.id, v.seq, fromHex(v.payloadHex));
      expect(toHex(got)).toBe(v.frameHex);
    });
  }
});

describe("decodeFrame — the valid vectors", () => {
  for (const v of V.frames.valid) {
    it(v.name, () => {
      const got = decodeFrame(fromHex(v.frameHex));
      expect(got.id).toBe(v.id);
      expect(got.seq).toBe(v.seq);
      expect(toHex(got.payload)).toBe(v.payloadHex);
    });
  }
});

describe("the generated vectors (a large payload)", () => {
  for (const v of V.frames.generated) {
    it(`${v.name} — roundtrip`, () => {
      const payload = buildPattern(v.pattern);
      expect(payload.byteLength).toBe(v.pattern.len);

      const frame = encodeFrame(v.id, v.seq, payload);
      expect(frame.byteLength).toBe(HEADER_BYTES + v.pattern.len);

      const back = decodeFrame(frame);
      expect(back.id).toBe(v.id);
      expect(back.seq).toBe(v.seq);
      expect(back.payload).toEqual(payload);
    });
  }
});

describe("encodeFrame — what has to be refused", () => {
  for (const v of V.frames.encodeInvalid) {
    it(`${v.name} → ${v.error}`, () => {
      const payload = buildPattern(v.pattern);
      try {
        encodeFrame(v.id, v.seq, payload);
        throw new Error("hata beklenirdi");
      } catch (err) {
        expect(err).toBeInstanceOf(FramingError);
        expect((err as FramingError).code).toBe(v.error);
      }
    });
  }
});

describe("decodeFrame — what has to be refused", () => {
  for (const v of V.frames.decodeInvalid) {
    it(`${v.name} → ${v.error}`, () => {
      let frame: Uint8Array;
      if (v.frame.hex !== undefined) {
        frame = fromHex(v.frame.hex);
      } else {
        const payload = buildPattern(v.frame.pattern as NonNullable<typeof v.frame.pattern>);
        const head = new Uint8Array(HEADER_BYTES);
        new DataView(head.buffer).setUint32(0, v.frame.id as number, false);
        new DataView(head.buffer).setUint32(4, v.frame.seq as number, false);
        frame = new Uint8Array(HEADER_BYTES + payload.byteLength);
        frame.set(head, 0);
        frame.set(payload, HEADER_BYTES);
      }

      try {
        decodeFrame(frame);
        throw new Error("hata beklenirdi");
      } catch (err) {
        expect(err).toBeInstanceOf(FramingError);
        expect((err as FramingError).code).toBe(v.error);
      }
    });
  }
});

describe("encodeFrame — the id/seq range", () => {
  it("it refuses values outside uint32", () => {
    const p = new Uint8Array(0);
    expect(() => encodeFrame(-1, 0, p)).toThrow(RangeError);
    expect(() => encodeFrame(0x1_0000_0000, 0, p)).toThrow(RangeError);
    expect(() => encodeFrame(1.5, 0, p)).toThrow(RangeError);
    expect(() => encodeFrame(0, -1, p)).toThrow(RangeError);
    expect(() => encodeFrame(0, 0x1_0000_0000, p)).toThrow(RangeError);
  });

  it("it accepts the boundary values", () => {
    expect(() => encodeFrame(0, 0, new Uint8Array(0))).not.toThrow();
    expect(() => encodeFrame(0xffffffff, 0xffffffff, new Uint8Array(0))).not.toThrow();
  });
});

describe("decodeFrame — the payload copy", () => {
  it("it keeps no reference to the frame buffer", () => {
    const frame = encodeFrame(1, 0, new Uint8Array([1, 2, 3]));
    const got = decodeFrame(frame);
    frame[HEADER_BYTES] = 99; // corrupt the source buffer
    expect(Array.from(got.payload)).toEqual([1, 2, 3]);
  });
});

describe("frameBody", () => {
  it("it yields no frame for an empty body", () => {
    expect([...frameBody(1, new Uint8Array(0))]).toEqual([]);
  });

  it("a body exactly at the limit goes into one frame", () => {
    const frames = [...frameBody(1, new Uint8Array(MAX_PAYLOAD))];
    expect(frames).toHaveLength(1);
    expect(frames[0]?.byteLength).toBe(MAX_FRAME);
  });

  it("one byte over the limit splits into two frames", () => {
    const frames = [...frameBody(1, new Uint8Array(MAX_PAYLOAD + 1))];
    expect(frames).toHaveLength(2);
    expect(decodeFrame(frames[0] as Uint8Array).payload.byteLength).toBe(MAX_PAYLOAD);
    expect(decodeFrame(frames[1] as Uint8Array).payload.byteLength).toBe(1);
  });

  it("it increments seq in order and preserves the content", () => {
    const body = buildPattern({ kind: "counter", len: MAX_PAYLOAD * 2 + 7 });
    const frames = [...frameBody(42, body, 3)];
    expect(frames).toHaveLength(3);

    const rebuilt = new Uint8Array(body.byteLength);
    let off = 0;
    frames.forEach((f, i) => {
      const d = decodeFrame(f);
      expect(d.id).toBe(42);
      expect(d.seq).toBe(3 + i);
      rebuilt.set(d.payload, off);
      off += d.payload.byteLength;
    });
    expect(off).toBe(body.byteLength);
    expect(rebuilt).toEqual(body);
  });
});
