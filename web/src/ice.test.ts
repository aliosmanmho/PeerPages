import { describe, expect, it } from "vitest";

import {
  DEFAULT_STUN,
  IceConfigError,
  iceServers,
  normalizeStunURL,
  routeFromStats,
  routeLabel,
  stunFromSearch,
} from "./ice";

describe("normalizeStunURL", () => {
  it("port verilmisse oldugu gibi birakir", () => {
    expect(normalizeStunURL("stun:stun.l.google.com:19302")).toBe("stun:stun.l.google.com:19302");
  });

  it("portsuz adrese varsayilani ekler", () => {
    expect(normalizeStunURL("stun:example.com")).toBe("stun:example.com:3478");
    expect(normalizeStunURL("stuns:example.com")).toBe("stuns:example.com:5349");
  });

  it("IPv6'da koseli parantezi bozmaz", () => {
    expect(normalizeStunURL("stun:[2001:db8::1]")).toBe("stun:[2001:db8::1]:3478");
    expect(normalizeStunURL("stun:[2001:db8::1]:3478")).toBe("stun:[2001:db8::1]:3478");
  });

  it("bosluklari kirpar", () => {
    expect(normalizeStunURL("  stun:example.com:3478 ")).toBe("stun:example.com:3478");
  });

  it("gecersiz adresleri reddeder", () => {
    for (const bad of ["", "   ", "example.com:3478", "turn:example.com", "http://example.com"]) {
      expect(() => normalizeStunURL(bad)).toThrow(IceConfigError);
    }
  });

  // TURN sayfaya HIC gelmiyor: kimlik bilgisi ajanda kaliyor (README §8).
  it("TURN adresini kabul etmez", () => {
    expect(() => normalizeStunURL("turn:turn.example.com:3478")).toThrow(IceConfigError);
  });

  it("yol ve query reddedilir", () => {
    expect(() => normalizeStunURL("stun:example.com:3478/yol")).toThrow(IceConfigError);
    expect(() => normalizeStunURL("stun:example.com:3478?transport=tcp")).toThrow(IceConfigError);
  });
});

describe("stunFromSearch", () => {
  it("parametre yoksa null doner (varsayilan liste)", () => {
    expect(stunFromSearch("")).toBeNull();
    expect(stunFromSearch("?relay=wss://nos.lol")).toBeNull();
  });

  it("tekrarlanan ve virgullu listeyi birlestirir", () => {
    const got = stunFromSearch(
      "?stun=stun:a.example.com:3478&stun=stun:b.example.com,stun:c.example.com",
    );
    expect(got).toEqual([
      "stun:a.example.com:3478",
      "stun:b.example.com:3478",
      "stun:c.example.com:3478",
    ]);
  });

  it("tekrarlari ayiklar", () => {
    expect(stunFromSearch("?stun=stun:a.example.com&stun=stun:a.example.com:3478")).toEqual([
      "stun:a.example.com:3478",
    ]);
  });

  // Testlerin yalnizca host adayi toplamasinin tek yolu bu.
  it("bos deger ICE sunucusunu kapatir", () => {
    expect(stunFromSearch("?stun=")).toEqual([]);
  });

  // Sessizce varsayilana donmek, kullanicinin verdigi listenin yok
  // sayildigini gizlerdi.
  it("gecersiz adres verilirse hata atar", () => {
    expect(() => stunFromSearch("?stun=bozuk")).toThrow(IceConfigError);
  });
});

describe("iceServers", () => {
  it("liste verilmezse varsayilani kullanir", () => {
    const got = iceServers(null);
    expect(got).toHaveLength(1);
    expect(got[0]?.urls).toEqual([...DEFAULT_STUN]);
  });

  it("bos liste hic sunucu vermez", () => {
    expect(iceServers([])).toEqual([]);
  });

  it("verilen listeyi kullanir", () => {
    expect(iceServers(["stun:a.example.com:3478"])[0]?.urls).toEqual(["stun:a.example.com:3478"]);
  });

  // Go tarafindaki ice.DefaultSTUN ile ayni olmali; NAT tahmini icin iki
  // sunucu gerekiyor.
  it("varsayilan listede iki sunucu var", () => {
    expect(DEFAULT_STUN.length).toBeGreaterThanOrEqual(2);
  });
});

/** getStats() cevabini taklit eder. */
function fakeStats(entries: Array<Record<string, unknown>>): RTCStatsReport {
  const map = new Map<string, Record<string, unknown>>();
  for (const e of entries) map.set(e.id as string, e);
  return map as unknown as RTCStatsReport;
}

describe("routeFromStats", () => {
  it("relay adayini TURN olarak tanir", () => {
    const stats = fakeStats([
      {
        id: "pair1",
        type: "candidate-pair",
        state: "succeeded",
        nominated: true,
        localCandidateId: "lc",
        remoteCandidateId: "rc",
      },
      { id: "lc", type: "local-candidate", candidateType: "relay" },
      { id: "rc", type: "remote-candidate", candidateType: "prflx" },
    ]);
    expect(routeFromStats(stats)).toBe("relay");
  });

  // Karsi taraf relay kullaniyorsa trafik yine relay'den geciyor: TURN
  // yalnizca AJAN tarafinda, yani beklenen durum tam olarak bu.
  it("uzak taraf relay ise relay doner", () => {
    const stats = fakeStats([
      {
        id: "p",
        type: "candidate-pair",
        state: "succeeded",
        nominated: true,
        localCandidateId: "l",
        remoteCandidateId: "r",
      },
      { id: "l", type: "local-candidate", candidateType: "srflx" },
      { id: "r", type: "remote-candidate", candidateType: "relay" },
    ]);
    expect(routeFromStats(stats)).toBe("relay");
  });

  it("host adayini dogrudan sayar", () => {
    const stats = fakeStats([
      {
        id: "p",
        type: "candidate-pair",
        selected: true,
        localCandidateId: "l",
        remoteCandidateId: "r",
      },
      { id: "l", type: "local-candidate", candidateType: "host" },
      { id: "r", type: "remote-candidate", candidateType: "host" },
    ]);
    expect(routeFromStats(stats)).toBe("host");
  });

  it("secilmemis ciftleri yok sayar", () => {
    const stats = fakeStats([
      {
        id: "p",
        type: "candidate-pair",
        state: "failed",
        nominated: false,
        localCandidateId: "l",
        remoteCandidateId: "r",
      },
      { id: "l", type: "local-candidate", candidateType: "relay" },
    ]);
    expect(routeFromStats(stats)).toBe("bilinmiyor");
  });

  it("bos raporda bilinmiyor doner", () => {
    expect(routeFromStats(fakeStats([]))).toBe("bilinmiyor");
  });

  it("aday kaydi eksikse bilinmiyor doner", () => {
    const stats = fakeStats([
      {
        id: "p",
        type: "candidate-pair",
        selected: true,
        localCandidateId: "yok",
        remoteCandidateId: "yok2",
      },
    ]);
    expect(routeFromStats(stats)).toBe("bilinmiyor");
  });
});

describe("routeLabel", () => {
  it("relay icin TURN'u adiyla soyler", () => {
    expect(routeLabel("relay")).toContain("TURN");
  });

  it("her yol icin metin var", () => {
    for (const r of ["host", "srflx", "prflx", "relay", "bilinmiyor"] as const) {
      expect(routeLabel(r).length).toBeGreaterThan(0);
    }
  });
});
