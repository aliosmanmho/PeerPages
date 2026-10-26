import { describe, expect, it } from "vitest";

import { PeerIDError, parseHash, shortenNpub } from "./peerid";

// Gercek bir npub (ajan tarafindan uretilmis, yalnizca test verisi).
const NPUB = "npub13e58y78ss5gn5yzdafm2vvpgrvh403pltm8c6th6m464yy8qp6kskfj7yy";

describe("parseHash", () => {
  it("tam npub'i cozer", () => {
    const got = parseHash(`#${NPUB}`);
    expect(got).not.toBeNull();
    expect(got?.npub).toBe(NPUB);
    expect(got?.secret).toBeNull();
    // pubkey 32 bayt hex olmali.
    expect(got?.pubkey).toMatch(/^[0-9a-f]{64}$/);
  });

  it("'#' olmadan da calisir", () => {
    expect(parseHash(NPUB)?.npub).toBe(NPUB);
  });

  it("bastaki/sondaki bosluklari tolere eder", () => {
    expect(parseHash(`  #${NPUB}  `)?.npub).toBe(NPUB);
  });

  it("sirri ayirir", () => {
    const got = parseHash(`#${NPUB}:kedi-balik-7`);
    expect(got?.npub).toBe(NPUB);
    expect(got?.secret).toBe("kedi-balik-7");
  });

  it("sir icinde ':' olabilir", () => {
    // Ilk ':' ayirac; gerisi sirra ait.
    expect(parseHash(`#${NPUB}:a:b:c`)?.secret).toBe("a:b:c");
  });

  it("bos hash icin null doner (hata degil)", () => {
    expect(parseHash("")).toBeNull();
    expect(parseHash("#")).toBeNull();
    expect(parseHash("   ")).toBeNull();
  });

  it("npub olmayan peer-id reddedilir", () => {
    expect(() => parseHash("#merhaba")).toThrow(PeerIDError);
    // nsec kazara paylasilirsa da reddedilmeli.
    expect(() => parseHash("#nsec1abcdef")).toThrow(PeerIDError);
  });

  it("checksum tutmayan npub reddedilir", () => {
    // Son karakteri degistirip bech32 checksum'ini bozuyoruz.
    const broken = `${NPUB.slice(0, -1)}${NPUB.endsWith("y") ? "z" : "y"}`;
    expect(() => parseHash(`#${broken}`)).toThrow(PeerIDError);
  });

  it("bos sir reddedilir", () => {
    expect(() => parseHash(`#${NPUB}:`)).toThrow(PeerIDError);
  });

  it("hata mesaji ne yapilacagini soyluyor", () => {
    try {
      parseHash("#bozuk");
      throw new Error("hata beklenirdi");
    } catch (err) {
      expect((err as Error).message).toMatch(/npub1/);
    }
  });
});

describe("shortenNpub", () => {
  it("ilk 12 ve son 4 karakteri korur", () => {
    const short = shortenNpub(NPUB);
    expect(short.startsWith(NPUB.slice(0, 12))).toBe(true);
    expect(short.endsWith(NPUB.slice(-4))).toBe(true);
    expect(short.length).toBeLessThan(NPUB.length);
  });

  it("kisa degeri degistirmez", () => {
    expect(shortenNpub("npub1kisa")).toBe("npub1kisa");
  });

  // Go tarafindaki identity.ShortenNpub ile ayni kurali uygulamali.
  it("Go tarafiyla ayni bicimi uretir", () => {
    expect(shortenNpub(NPUB)).toBe(`${NPUB.slice(0, 12)}…${NPUB.slice(-4)}`);
  });
});
