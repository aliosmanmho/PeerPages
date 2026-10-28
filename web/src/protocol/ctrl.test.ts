import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { CtrlError, type CtrlMsg, canonicalCtrl, parseCtrl } from "./ctrl";
import type { TestVectors } from "./vectors";

const vectorsPath = fileURLToPath(new URL("../../../protocol/testvectors.json", import.meta.url));
const V = JSON.parse(readFileSync(vectorsPath, "utf8")) as TestVectors;

describe("parseCtrl + canonicalCtrl — the valid vectors", () => {
  for (const v of V.ctrl.valid) {
    it(v.name, () => {
      const msg = parseCtrl(v.text);
      // The canonical output has to be byte-for-byte the same as the Go
      // side's.
      expect(canonicalCtrl(msg)).toBe(v.canonical);
      // Reparsing the canonical text has to give the same message.
      expect(parseCtrl(canonicalCtrl(msg))).toEqual(msg);
    });
  }

  it("it covers every message type", () => {
    const seen = new Set(V.ctrl.valid.map((v) => (parseCtrl(v.text) as CtrlMsg).t));
    expect([...seen].sort()).toEqual([
      "abort",
      "end",
      "hello",
      "ping",
      "pong",
      "req",
      "res",
      "ws-close",
      "ws-ok",
      "ws-open",
    ]);
  });
});

describe("parseCtrl — what has to be refused", () => {
  for (const v of V.ctrl.invalid) {
    it(`${v.name} → ${v.error}`, () => {
      try {
        const got = parseCtrl(v.text);
        throw new Error(`hata beklenirdi, donen: ${JSON.stringify(got)}`);
      } catch (err) {
        expect(err).toBeInstanceOf(CtrlError);
        expect((err as CtrlError).code).toBe(v.error);
      }
    });
  }
});

describe("parseCtrl — the field details", () => {
  it("it reads the req fields correctly", () => {
    const msg = parseCtrl(
      '{"t":"req","id":17,"method":"GET","path":"/api/items?x=1","headers":{"Accept":"text/html"},"hasBody":false}',
    );
    expect(msg).toEqual({
      t: "req",
      id: 17,
      method: "GET",
      path: "/api/items?x=1",
      headers: { accept: "text/html" }, // baslik adlari kucuk harfe normalize
      hasBody: false,
    });
  });

  it("it reads the res fields correctly", () => {
    expect(parseCtrl('{"t":"res","id":3,"status":404,"headers":{}}')).toEqual({
      t: "res",
      id: 3,
      status: 404,
      headers: {},
    });
  });

  it("it reads the hello fields correctly", () => {
    expect(
      parseCtrl('{"t":"hello","v":1,"targets":["default","admin"],"auth":"required"}'),
    ).toEqual({
      t: "hello",
      v: 1,
      targets: ["default", "admin"],
      auth: "required",
    });
  });

  it("ping/pong carry no id", () => {
    expect(parseCtrl('{"t":"ping"}')).toEqual({ t: "ping" });
    expect(parseCtrl('{"t":"pong"}')).toEqual({ t: "pong" });
  });

  it("it ignores unknown extra fields", () => {
    expect(parseCtrl('{"t":"end","id":1,"frames":0,"len":0,"gelecekteAlan":true}')).toEqual({
      t: "end",
      id: 1,
      frames: 0,
      len: 0,
    });
  });

  it("it reads the end counters", () => {
    expect(parseCtrl('{"t":"end","id":2,"frames":6,"len":90000}')).toEqual({
      t: "end",
      id: 2,
      frames: 6,
      len: 90000,
    });
  });
});

describe("canonicalCtrl", () => {
  it("it sorts the headers keys", () => {
    const msg = parseCtrl(
      '{"t":"req","id":1,"method":"GET","path":"/","headers":{"z":"3","a":"1","m":"2"},"hasBody":false}',
    );
    expect(canonicalCtrl(msg)).toBe(
      '{"t":"req","id":1,"method":"GET","path":"/","headers":{"a":"1","m":"2","z":"3"},"hasBody":false}',
    );
  });

  it("it is written even when hasBody is false", () => {
    expect(
      canonicalCtrl({ t: "req", id: 0, method: "GET", path: "/", headers: {}, hasBody: false }),
    ).toBe('{"t":"req","id":0,"method":"GET","path":"/","headers":{},"hasBody":false}');
  });

  it("it does not escape HTML characters", () => {
    const msg = parseCtrl('{"t":"abort","id":1,"reason":"a<b>&c"}');
    expect(canonicalCtrl(msg)).toBe('{"t":"abort","id":1,"reason":"a<b>&c"}');
  });

  it("it does not escape non-ASCII characters", () => {
    const msg = parseCtrl('{"t":"abort","id":1,"reason":"çğü"}');
    expect(canonicalCtrl(msg)).toBe('{"t":"abort","id":1,"reason":"çğü"}');
  });
});
