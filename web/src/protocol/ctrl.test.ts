import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { CtrlError, type CtrlMsg, canonicalCtrl, parseCtrl } from "./ctrl";
import type { TestVectors } from "./vectors";

const vectorsPath = fileURLToPath(new URL("../../../protocol/testvectors.json", import.meta.url));
const V = JSON.parse(readFileSync(vectorsPath, "utf8")) as TestVectors;

describe("parseCtrl + canonicalCtrl — gecerli vektorler", () => {
  for (const v of V.ctrl.valid) {
    it(v.name, () => {
      const msg = parseCtrl(v.text);
      // Kanonik cikti Go tarafiyla bayt bayt ayni olmali.
      expect(canonicalCtrl(msg)).toBe(v.canonical);
      // Kanonik metin yeniden ayristirildiginda ayni mesaji vermeli.
      expect(parseCtrl(canonicalCtrl(msg))).toEqual(msg);
    });
  }

  it("tum mesaj tiplerini kapsiyor", () => {
    const seen = new Set(V.ctrl.valid.map((v) => (parseCtrl(v.text) as CtrlMsg).t));
    expect([...seen].sort()).toEqual(["abort", "end", "hello", "ping", "pong", "req", "res"]);
  });
});

describe("parseCtrl — reddedilmesi gerekenler", () => {
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

describe("parseCtrl — alan ayrintilari", () => {
  it("req alanlarini dogru okur", () => {
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

  it("res alanlarini dogru okur", () => {
    expect(parseCtrl('{"t":"res","id":3,"status":404,"headers":{}}')).toEqual({
      t: "res",
      id: 3,
      status: 404,
      headers: {},
    });
  });

  it("hello alanlarini dogru okur", () => {
    expect(
      parseCtrl('{"t":"hello","v":1,"targets":["default","admin"],"auth":"required"}'),
    ).toEqual({
      t: "hello",
      v: 1,
      targets: ["default", "admin"],
      auth: "required",
    });
  });

  it("ping/pong id tasimaz", () => {
    expect(parseCtrl('{"t":"ping"}')).toEqual({ t: "ping" });
    expect(parseCtrl('{"t":"pong"}')).toEqual({ t: "pong" });
  });

  it("bilinmeyen fazladan alanlari yok sayar", () => {
    expect(parseCtrl('{"t":"end","id":1,"gelecekteAlan":true}')).toEqual({ t: "end", id: 1 });
  });
});

describe("canonicalCtrl", () => {
  it("headers anahtarlarini siralar", () => {
    const msg = parseCtrl(
      '{"t":"req","id":1,"method":"GET","path":"/","headers":{"z":"3","a":"1","m":"2"},"hasBody":false}',
    );
    expect(canonicalCtrl(msg)).toBe(
      '{"t":"req","id":1,"method":"GET","path":"/","headers":{"a":"1","m":"2","z":"3"},"hasBody":false}',
    );
  });

  it("hasBody false olsa bile yazilir", () => {
    expect(
      canonicalCtrl({ t: "req", id: 0, method: "GET", path: "/", headers: {}, hasBody: false }),
    ).toBe('{"t":"req","id":0,"method":"GET","path":"/","headers":{},"hasBody":false}');
  });

  it("HTML karakterlerini kacirmaz", () => {
    const msg = parseCtrl('{"t":"abort","id":1,"reason":"a<b>&c"}');
    expect(canonicalCtrl(msg)).toBe('{"t":"abort","id":1,"reason":"a<b>&c"}');
  });

  it("ASCII disi karakterleri kacirmaz", () => {
    const msg = parseCtrl('{"t":"abort","id":1,"reason":"çğü"}');
    expect(canonicalCtrl(msg)).toBe('{"t":"abort","id":1,"reason":"çğü"}');
  });
});
