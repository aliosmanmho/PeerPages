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
  MAX_TARGET_NAME,
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
  validTargetName,
} from "./signaling";

// The Go side (agent/internal/signaling/vectors_test.go) reads the SAME
// file.
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
    maxTargetName: number;
  };
  targetNamePattern: string;
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
  it("it reports the same values as the TS constants", () => {
    expect(V.version).toBe(SIGNALING_VERSION);
    expect(V.limits.sidLength).toBe(SID_LENGTH);
    expect(V.limits.maxSDP).toBe(MAX_SDP);
    expect(V.limits.maxCandidate).toBe(MAX_CANDIDATE);
    expect(V.limits.maxMid).toBe(MAX_MID);
    expect(V.limits.maxReason).toBe(MAX_REASON);
    expect(V.limits.maxTargetName).toBe(MAX_TARGET_NAME);
    expect(V.secret.prefix).toBe(SECRET_PREFIX);
    expect(V.secret.nonceBytes).toBe(12);
  });

  it("it holds the expected number of vectors", () => {
    expect(V.messages.valid.length).toBeGreaterThanOrEqual(8);
    expect(V.messages.invalid.length).toBeGreaterThanOrEqual(25);
    expect(V.messages.generated.length).toBeGreaterThanOrEqual(8);
    expect(V.sealed.valid.length).toBeGreaterThanOrEqual(3);
    expect(V.sealed.invalid.length).toBeGreaterThanOrEqual(4);
  });
});

describe("parseSignaling + canonicalSignaling — the valid vectors", () => {
  for (const vec of V.messages.valid) {
    it(vec.name, () => {
      const msg = parseSignaling(vec.text);
      // The canonical output has to be byte-for-byte the same as the Go
      // side's.
      expect(canonicalSignaling(msg)).toBe(vec.canonical);
      // Reparsing the canonical text has to give the same message.
      expect(parseSignaling(canonicalSignaling(msg))).toEqual(msg);
    });
  }

  it("it covers every message type", () => {
    const seen = new Set(V.messages.valid.map((v) => parseSignaling(v.text).t));
    expect([...seen].sort()).toEqual(["answer", "bye", "candidate", "offer"]);
  });
});

describe("parseSignaling — what has to be refused", () => {
  for (const vec of V.messages.invalid) {
    it(`${vec.name} → ${vec.error}`, () => {
      expect(codeOf(() => parseSignaling(vec.text))).toBe(vec.error);
    });
  }
});

describe("the boundary cases", () => {
  const sid = "0123456789abcdef";

  for (const vec of V.messages.generated) {
    it(`${vec.name} (${vec.len} bayt → ${vec.accept ? "accepted" : "refused"})`, () => {
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
          throw new Error(`unknown boundary case: ${vec.kind}`);
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

  // The length has to be measured in BYTES: the Go side counts bytes
  // while JS .length counts UTF-16 code units. If the two drift apart,
  // the sides decide differently about the same message.
  it("the length is measured in bytes (non-ASCII)", () => {
    const sid = "0123456789abcdef";
    // "é" is 2 bytes in UTF-8: 33 characters = 66 bytes > the 64 limit.
    const reason = "é".repeat(33);
    expect(reason.length).toBeLessThanOrEqual(MAX_REASON);
    expect(new TextEncoder().encode(reason).byteLength).toBeGreaterThan(MAX_REASON);
    expect(() => validateSignaling({ t: "bye", sid, reason })).toThrow();
  });
});

describe("sid", () => {
  it("newSID produces a valid form", () => {
    for (let i = 0; i < 20; i++) {
      const sid = newSID();
      expect(sid).toHaveLength(SID_LENGTH);
      expect(validSID(sid)).toBe(true);
    }
  });

  it("newSID differs on every call", () => {
    const seen = new Set(Array.from({ length: 50 }, () => newSID()));
    expect(seen.size).toBe(50);
  });

  it("validSID validates the form correctly", () => {
    expect(validSID("0123456789abcdef")).toBe(true);
    expect(validSID("0123456789ABCDEF")).toBe(false); // uppercase
    expect(validSID("0123456789abcde")).toBe(false); // kisa
    expect(validSID("0123456789abcdef0")).toBe(false); // uzun
    expect(validSID("0123456789abcdeg")).toBe(false); // not hex
    expect(validSID("")).toBe(false);
  });
});

describe("Sealer — the shared secret layer", () => {
  it("the key derivation matches the vector", () => {
    const s = Sealer.fromSecret(V.sealed.key.secret);
    expect(s.keyHex()).toBe(V.sealed.key.keyHex);
  });

  it("an empty secret is refused", () => {
    expect(() => Sealer.fromSecret("")).toThrow();
  });

  for (const vec of V.sealed.valid) {
    it(`${vec.name} — vektordeki payload cozuluyor`, () => {
      const s = Sealer.fromSecret(vec.secret);
      expect(s.open(vec.payload)).toBe(vec.plaintext);
      // It also has to parse as a message.
      expect(() => s.decode(vec.payload)).not.toThrow();
    });

    // With the same nonce it has to produce a BYTE-FOR-BYTE identical
    // payload: the proof that the two implementations use the same
    // framing.
    it(`${vec.name} — the same nonce produces the same payload`, () => {
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

  // "Refuse unsealed offers": plain JSON arriving where a secret is
  // expected is refused.
  it("a plain payload is refused where a secret is expected", () => {
    const v = V.sealed.plainWhenSecretExpected;
    const s = Sealer.fromSecret(v.secret);
    expect(codeOf(() => s.open(v.payload))).toBe(v.error);
  });

  // A sealed payload arriving with no secret is refused (we cannot open
  // it).
  it("with no secret a sealed payload is refused", () => {
    const v = V.sealed.sealedWhenNoSecret;
    expect(codeOf(() => plainCodec.decode(v.payload))).toBe(v.error);
  });

  it("the round trip works with a random nonce", () => {
    const s = Sealer.fromSecret("bir-sir");
    const msg: SignalingMsg = { t: "bye", sid: newSID(), reason: "visitor-left" };
    const payload = s.encode(msg);
    expect(isSealed(payload)).toBe(true);
    expect(s.decode(payload)).toEqual(msg);
  });

  it("the same message produces a different payload every time (the nonce is random)", () => {
    const s = Sealer.fromSecret("bir-sir");
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    expect(s.encode(msg)).not.toBe(s.encode(msg));
  });
});

describe("codecFor", () => {
  it("with no secret it produces plain JSON", () => {
    const codec = codecFor(null);
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    const payload = codec.encode(msg);
    expect(isSealed(payload)).toBe(false);
    expect(payload).toBe(canonicalSignaling(msg));
    expect(codec.decode(payload)).toEqual(msg);
  });

  it("with a secret it produces a sealed payload", () => {
    const codec = codecFor("sir");
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    const payload = codec.encode(msg);
    expect(isSealed(payload)).toBe(true);
    expect(codec.decode(payload)).toEqual(msg);
  });

  // The sides disagreeing about the secret must not pass silently.
  it("the sealed and unsealed sides refuse each other", () => {
    const withSecret = codecFor("sir");
    const without = codecFor(null);
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };

    expect(() => without.decode(withSecret.encode(msg))).toThrow();
    expect(() => withSecret.decode(without.encode(msg))).toThrow();
  });

  it("different secrets refuse each other", () => {
    const a = codecFor("sir-a");
    const b = codecFor("sir-b");
    const msg: SignalingMsg = { t: "bye", sid: "0123456789abcdef", reason: "x" };
    expect(() => b.decode(a.encode(msg))).toThrow();
  });
});

describe("offer.target (phase 3)", () => {
  const sid = "0123456789abcdef";
  const sdp = "v=0\r\n";

  it("the name rule matches the pattern in the vectors", () => {
    expect(new RegExp(V.targetNamePattern).source).toBe(/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$/.source);
  });

  // This matters: the canonical form of a target-less offer has to be
  // the SAME as it was before the field existed, or the old vectors and
  // running agents break.
  it("with no target the field is not written into the canonical form", () => {
    expect(canonicalSignaling({ t: "offer", sid, sdp })).toBe(
      `{"v":1,"t":"offer","sid":"${sid}","sdp":"v=0\\r\\n"}`,
    );
  });

  it("a target comes after sdp", () => {
    expect(canonicalSignaling({ t: "offer", sid, sdp, target: "admin" })).toBe(
      `{"v":1,"t":"offer","sid":"${sid}","sdp":"v=0\\r\\n","target":"admin"}`,
    );
  });

  it("the target survives the round trip", () => {
    const msg: SignalingMsg = { t: "offer", sid, sdp, target: "web-2_eski" };
    expect(parseSignaling(canonicalSignaling(msg))).toEqual(msg);
  });

  it("it accepts the valid names and refuses the invalid ones", () => {
    for (const name of ["a", "A", "0", "default", "admin", "web-2_eski", "a".repeat(32)]) {
      expect(validTargetName(name)).toBe(true);
    }
    for (const name of ["", "-a", "_a", "a/b", "a.b", "a b", " a", "a ", "a".repeat(33), "tr-c"]) {
      expect(validTargetName(name)).toBe(name === "tr-c");
    }
  });

  it("a message with an invalid target is refused in validation", () => {
    expect(() => validateSignaling({ t: "offer", sid, sdp, target: "a/b" })).toThrow();
    expect(() => validateSignaling({ t: "offer", sid, sdp, target: "" })).toThrow();
  });

  // target only means anything on an offer; it is not read on the other
  // types.
  it("a target in an answer is ignored", () => {
    const text = `{"v":1,"t":"answer","sid":"${sid}","sdp":"v=0","target":"admin"}`;
    expect(parseSignaling(text)).toEqual({ t: "answer", sid, sdp: "v=0" });
  });
});
