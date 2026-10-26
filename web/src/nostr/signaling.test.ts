import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  canonicalSignaling,
  codecFor,
  isSealed,
  MAX_CANDIDATE,
  MAX_MID,
  MAX_REASON,
  MAX_SDP,
  newSID,
  parseSignaling,
  plainCodec,
  SECRET_PREFIX,
  Sealer,
  SID_LENGTH,
  SIGNALING_VERSION,
  type SignalingError,
  type SignalingMsg,
  validateSignaling,
  validSID,
} from "./signaling";

// Go tarafi (agent/internal/signaling/vectors_test.go) AYNI dosyayi okur.
const vectorsPath = fileURLToPath(
  new URL("../../../protocol/signaling-testvectors.json", import.meta.url),
);

interface Vectors {
  version: number;
  limits: {
    sidLength: number;
    maxSDP: number;
    maxCandidate: number;
    maxMid: number;
    maxReason: number;
  };
  secret: {
    prefix: string;
    salt: string;
    info: string;
    aad: string;
    nonceBytes: number;
  };
  messages: {
    valid: Array<{ name: string; text: string; canonical: string }>;
    invalid: Array<{ name: string; text: string; error: string }>;
    generated: Array<{ name: string; kind: string; len: number; accept: boolean }>;
  };
  sealed: {
    key: { secret: string; keyHex: string };
    valid: Array<{
      name: string;
      secret: string;
      nonceHex: string;
      plaintext: string;
      payload: string;
    }>;
    invalid: Array<{ name: string; secret: string; payload: string; error: string }>;
    plainWhenSecretExpected: { secret: string; payload: string; error: string };
    sealedWhenNoSecret: { payload: string; error: string };
  };
}

const V = JSON.parse(readFileSync(vectorsPath, "utf8")) as Vectors;

function hexBytes(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function codeOf(fn: () => unknown): string {
  try {
    fn();
  } catch (err) {
    return (err as SignalingError).code ?? `BEKLENMEYEN: ${String(err)}`;
  }
  return "HATA_YOK";
}

describe("signaling-testvectors.json", () => {
  it("TS sabitleriyle ayni degerleri bildiriyor", () => {
    expect(V.version).toBe(SIGNALING_VERSION);
    expect(V.limits.sidLength).toBe(SID_LENGTH);
    expect(V.limits.maxSDP).toBe(MAX_SDP);
    expect(V.limits.maxCandidate).toBe(MAX_CANDIDATE);
    expect(V.limits.maxMid).toBe(MAX_MID);
    expect(V.limits.maxReason).toBe(MAX_REASON);
    expect(V.secret.prefix).toBe(SECRET_PREFIX);
    expect(V.secret.nonceBytes).toBe(12);
  });

  it("beklenen sayida vektor iceriyor", () => {
    expect(V.messages.valid.length).toBeGreaterThanOrEqual(8);
    expect(V.messages.invalid.length).toBeGreaterThanOrEqual(25);
    expect(V.messages.generated.length).toBeGreaterThanOrEqual(8);
    expect(V.sealed.valid.length).toBeGreaterThanOrEqual(3);
    expect(V.sealed.invalid.length).toBeGreaterThanOrEqual(4);
  });
});

describe("parseSignaling + canonicalSignaling — gecerli vektorler", () => {
  for (const vec of V.messages.valid) {
    it(vec.name, () => {
      const msg = parseSignaling(vec.text);
      // Kanonik cikti Go tarafiyla bayt bayt ayni olmali.
      expect(canonicalSignaling(msg)).toBe(vec.canonical);
      // Kanonik metin yeniden ayristirildiginda ayni mesaji vermeli.
      expect(parseSignaling(canonicalSignaling(msg))).toEqual(msg);
    });
  }

  it("tum mesaj tiplerini kapsiyor", () => {
    const seen = new Set(V.messages.valid.map((v) => parseSignaling(v.text).t));
    expect([...seen].sort()).toEqual(["answer", "bye", "candidate", "offer"]);
  });
});

describe("parseSignaling — reddedilmesi gerekenler", () => {
  for (const vec of V.messages.invalid) {
    it(`${vec.name} → ${vec.error}`, () => {
      expect(codeOf(() => parseSignaling(vec.text))).toBe(vec.error);
    });
  }
});

describe("sinir vakalari", () => {
  const sid = "0123456789abcdef";

  for (const vec of V.messages.generated) {
    it(`${vec.name} (${vec.len} bayt → ${vec.accept ? "kabul" : "red"})`, () => {
      const filler = "x".repeat(vec.len);
      let msg: SignalingMsg;
      switch (vec.kind) {
        case "offer-sdp":
          msg = { t: "offer", sid, sdp: filler };
          break;
        case "candidate-cand":
          msg = { t: "candidate", sid, cand: filler, mid: "0" };
          break;
        case "candidate-mid":
          msg = { t: "candidate", sid, cand: "candidate:1", mid: filler };
          break;
        case "bye-reason":
          msg = { t: "bye", sid, reason: filler };
          break;
        default:
          throw new Error(`bilinmeyen sinir vakasi: ${vec.kind}`);
      }

      if (vec.accept) {
        expect(() => validateSignaling(msg)).not.toThrow();
        expect(() => parseSignaling(canonicalSignaling(msg))).not.toThrow();
      } else {
        expect(() => validateSignaling(msg)).toThrow();
        expect(() => parseSignaling(canonicalSignaling(msg))).toThrow();
      }
    });
  }

  // Uzunluk BAYT cinsinden olculmeli: Go tarafi bayt sayiyor, JS'in .length'i
  // UTF-16 kod birimi sayar. Ikisi ayrilirsa taraflar ayni mesaj icin farkli
  // karar verir.
  it("uzunluk bayt cinsinden olculuyor (ASCII disi)", () => {
    const sid = "0123456789abcdef";
    // "ç" UTF-8'de 2 bayt: 33 karakter = 66 bayt > 64 sinir.
    const reason = "ç".repeat(33);
    expect(reason.length).toBeLessThanOrEqual(MAX_REASON);
    expect(new TextEncoder().encode(reason).byteLength).toBeGreaterThan(MAX_REASON);
    expect(() => validateSignaling({ t: "bye", sid, reason })).toThrow();
  });
});

describe("sid", () => {
  it("newSID gecerli bicimde uretir", () => {
    for (let i = 0; i < 20; i++) {
      const sid = newSID();
      expect(sid).toHaveLength(SID_LENGTH);
      expect(validSID(sid)).toBe(true);
    }
  });

  it("newSID her cagrida farkli", () => {
    const seen = new Set(Array.from({ length: 50 }, () => newSID()));
    expect(seen.size).toBe(50);
  });

  it("validSID bicimi dogru dogruluyor", () => {
    expect(validSID("0123456789abcdef")).toBe(true);
    expect(validSID("0123456789ABCDEF")).toBe(false); // buyuk harf
    expect(validSID("0123456789abcde")).toBe(false); // kisa
    expect(validSID("0123456789abcdef0")).toBe(false); // uzun
    expect(validSID("0123456789abcdeg")).toBe(false); // hex degil
    expect(validSID("")).toBe(false);
  });
});

describe("Sealer — paylasilan sir katmani", () => {
  it("anahtar turetimi vektorle ayni", () => {
    const s = Sealer.fromSecret(V.sealed.key.secret);
    expect(s.keyHex()).toBe(V.sealed.key.keyHex);
  });

  it("bos sir reddedilir", () => {
    expect(() => Sealer.fromSecret("")).toThrow();
  });

  for (const vec of V.sealed.valid) {
    it(`${vec.name} — vektordeki payload cozuluyor`, () => {
      const s = Sealer.fromSecret(vec.secret);
      expect(s.open(vec.payload)).toBe(vec.plaintext);
      // Mesaj olarak da ayristirilabilmeli.
      expect(() => s.decode(vec.payload)).not.toThrow();
    });

    // Ayni nonce ile BAYT BAYT ayni payload uretilmeli: iki uygulamanin
    // ayni cercevelemeyi kullandiginin kaniti.
    it(`${vec.name} — ayni nonce ile ayni payload uretiliyor`, () => {
      const s = Sealer.fromSecret(vec.secret);
      expect(s.sealWithNonce(hexBytes(vec.nonceHex), vec.plaintext)).toBe(vec.payload);
    });
  }

  for (const vec of V.sealed.invalid) {
    it(`${vec.name} → ${vec.error}`, () => {
      const s = Sealer.fromSecret(vec.secret);
      expect(codeOf(() => s.open(vec.payload))).toBe(vec.error);
    });
  }

  // "Sirsiz teklifleri reddet": sir beklenirken duz JSON gelirse red.
  it("sir beklenirken duz payload reddedilir", () => {
    const v = V.sealed.plainWhenSecretExpected;
    const s = Sealer.fromSecret(v.secret);
    expect(codeOf(() => s.open(v.payload))).toBe(v.error);
  });

  // Sir yokken sirli payload gelirse red (cozemeyiz).
  it("sir yokken sirli payload reddedilir", () => {
    const v = V.sealed.sealedWhenNoSecret;
    expect(codeOf(() => plainCodec.decode(v.payload))).toBe(v.error);
  });

  it("rastgele nonce ile roundtrip calisiyor", () => {
    const s = Sealer.fromSecret("bir-sir");
    const msg: SignalingMsg = { t: "bye", sid: newSID(), reason: "visitor-left" };
    const payload = s.encode(msg);
    expect(isSealed(payload)).toBe(true);
    expect(s.decode(payload)).toEqual(msg);
  });

  it("ayni mesaj her seferinde farkli payload uretir (nonce rastgele)", () => {
    const s = Sealer.fromSecret("bir-sir");
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    expect(s.encode(msg)).not.toBe(s.encode(msg));
  });
});

describe("codecFor", () => {
  it("sir yoksa duz JSON uretir", () => {
    const codec = codecFor(null);
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    const payload = codec.encode(msg);
    expect(isSealed(payload)).toBe(false);
    expect(payload).toBe(canonicalSignaling(msg));
    expect(codec.decode(payload)).toEqual(msg);
  });

  it("sir varsa sirli payload uretir", () => {
    const codec = codecFor("sir");
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    const payload = codec.encode(msg);
    expect(isSealed(payload)).toBe(true);
    expect(codec.decode(payload)).toEqual(msg);
  });

  // Taraflarin sir konusunda anlasmamasi sessizce gecmemeli.
  it("sirli ve sirsiz taraflar birbirini reddeder", () => {
    const withSecret = codecFor("sir");
    const without = codecFor(null);
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };

    expect(() => without.decode(withSecret.encode(msg))).toThrow();
    expect(() => withSecret.decode(without.encode(msg))).toThrow();
  });

  it("farkli sirlar birbirini reddeder", () => {
    const a = codecFor("sir-a");
    const b = codecFor("sir-b");
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    expect(() => b.decode(a.encode(msg))).toThrow();
  });
});
