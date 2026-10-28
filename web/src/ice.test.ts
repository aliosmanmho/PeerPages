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
  it("with a port given it leaves it as it is", () => {
    expect(normalizeStunURL("stun:stun.l.google.com:19302")).toBe("stun:stun.l.google.com:19302");
  });

  it("it adds the default to an address with no port", () => {
    expect(normalizeStunURL("stun:example.com")).toBe("stun:example.com:3478");
    expect(normalizeStunURL("stuns:example.com")).toBe("stuns:example.com:5349");
  });

  it("it does not break the brackets on IPv6", () => {
    expect(normalizeStunURL("stun:[2001:db8::1]")).toBe("stun:[2001:db8::1]:3478");
    expect(normalizeStunURL("stun:[2001:db8::1]:3478")).toBe("stun:[2001:db8::1]:3478");
  });

  it("it trims whitespace", () => {
    expect(normalizeStunURL("  stun:example.com:3478 ")).toBe("stun:example.com:3478");
  });

  it("it refuses invalid addresses", () => {
    for (const bad of ["", "   ", "example.com:3478", "turn:example.com", "http://example.com"]) {
      expect(() => normalizeStunURL(bad)).toThrow(IceConfigError);
    }
  });

  // TURN NEVER reaches the page: the credentials stay on the agent
  // (BRIEF section 8).
  it("it does not accept a TURN address", () => {
    expect(() => normalizeStunURL("turn:turn.example.com:3478")).toThrow(IceConfigError);
  });

  it("a path and a query are refused", () => {
    expect(() => normalizeStunURL("stun:example.com:3478/yol")).toThrow(IceConfigError);
    expect(() => normalizeStunURL("stun:example.com:3478?transport=tcp")).toThrow(IceConfigError);
  });
});

describe("stunFromSearch", () => {
  it("with no parameter it returns null (the default list)", () => {
    expect(stunFromSearch("")).toBeNull();
    expect(stunFromSearch("?relay=wss://nos.lol")).toBeNull();
  });

  it("it merges a repeated and comma-separated list", () => {
    const got = stunFromSearch(
      "?stun=stun:a.example.com:3478&stun=stun:b.example.com,stun:c.example.com",
    );
    expect(got).toEqual([
      "stun:a.example.com:3478",
      "stun:b.example.com:3478",
      "stun:c.example.com:3478",
    ]);
  });

  it("it removes duplicates", () => {
    expect(stunFromSearch("?stun=stun:a.example.com&stun=stun:a.example.com:3478")).toEqual([
      "stun:a.example.com:3478",
    ]);
  });

  // That is the only way for the tests to gather host candidates only.
  it("an empty value turns the ICE server off", () => {
    expect(stunFromSearch("?stun=")).toEqual([]);
  });

  // Falling back to the default silently would hide that the list the
  // user gave was ignored.
  it("it throws when given an invalid address", () => {
    expect(() => stunFromSearch("?stun=bozuk")).toThrow(IceConfigError);
  });
});

describe("iceServers", () => {
  it("with no list given it uses the default", () => {
    const got = iceServers(null);
    expect(got).toHaveLength(1);
    expect(got[0]?.urls).toEqual([...DEFAULT_STUN]);
  });

  it("an empty list gives no server at all", () => {
    expect(iceServers([])).toEqual([]);
  });

  it("it uses the given list", () => {
    expect(iceServers(["stun:a.example.com:3478"])[0]?.urls).toEqual(["stun:a.example.com:3478"]);
  });

  // It has to match ice.DefaultSTUN on the Go side; the NAT guess needs
  // two servers.
  it("the default list holds two servers", () => {
    expect(DEFAULT_STUN.length).toBeGreaterThanOrEqual(2);
  });
});

/** Imitates a getStats() answer. */
function fakeStats(entries: Array<Record<string, unknown>>): RTCStatsReport {
  const map = new Map<string, Record<string, unknown>>();
  for (const e of entries) map.set(e.id as string, e);
  return map as unknown as RTCStatsReport;
}

describe("routeFromStats", () => {
  it("it recognises a relay candidate as TURN", () => {
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

  // If the other side uses a relay the traffic still goes over the
  // relay: TURN is on the AGENT side only, so this is exactly the
  // expected case.
  it("when the remote side is a relay it returns relay", () => {
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

  it("it counts a host candidate as direct", () => {
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

  it("it ignores the pairs that were not selected", () => {
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
    expect(routeFromStats(stats)).toBe("unknown");
  });

  it("on an empty report it returns unknown", () => {
    expect(routeFromStats(fakeStats([]))).toBe("unknown");
  });

  it("with the candidate record missing it returns unknown", () => {
    const stats = fakeStats([
      {
        id: "p",
        type: "candidate-pair",
        selected: true,
        localCandidateId: "yok",
        remoteCandidateId: "yok2",
      },
    ]);
    expect(routeFromStats(stats)).toBe("unknown");
  });
});

describe("routeLabel", () => {
  it("for a relay it names TURN", () => {
    expect(routeLabel("relay")).toContain("TURN");
  });

  it("there is a text for every route", () => {
    for (const r of ["host", "srflx", "prflx", "relay", "unknown"] as const) {
      expect(routeLabel(r).length).toBeGreaterThan(0);
    }
  });
});
