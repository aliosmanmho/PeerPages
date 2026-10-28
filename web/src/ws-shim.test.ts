import { describe, expect, it } from "vitest";

import { isTunneled, localPath, scopeFromSrc } from "./ws-shim";

/**
 * The shim's pure helpers. The shim itself (the window.WebSocket wrapper)
 * needs a browser, so it is verified in e2e (web/e2e/phase6.spec.ts);
 * what is tested here is the logic that makes the decisions.
 */

describe("scopeFromSrc", () => {
  it("it takes the shim's directory as the scope root", () => {
    expect(scopeFromSrc("https://x.github.io/PeerPages/ws-shim.js", "https://x.github.io/")).toBe(
      "/PeerPages/",
    );
  });

  it("served at the root it is /", () => {
    expect(scopeFromSrc("https://x.io/ws-shim.js", "https://x.io/")).toBe("/");
  });

  it("it resolves a relative src", () => {
    expect(scopeFromSrc("ws-shim.js", "https://x.io/PeerPages/sayfa")).toBe("/PeerPages/");
  });

  // Inventing a scope from an unexpected src means trimming paths
  // wrongly: the safe side is "/".
  it("with an unexpected file name it returns /", () => {
    expect(scopeFromSrc("https://x.io/PeerPages/baska.js", "https://x.io/")).toBe("/");
    expect(scopeFromSrc(undefined, "https://x.io/")).toBe("/");
    expect(scopeFromSrc("::::", "not-a-url")).toBe("/");
  });
});

describe("isTunneled", () => {
  it("the same hostname enters the tunnel", () => {
    expect(isTunneled(new URL("wss://x.github.io/ws"), "x.github.io")).toBe(true);
  });

  // Vite HMR can put its own port in the address; that port is
  // meaningless in the visitor's browser, and the agent's allowlist
  // decides the target.
  it("a different port does not matter", () => {
    expect(isTunneled(new URL("wss://x.github.io:5173/"), "x.github.io")).toBe(true);
  });

  // The shim's counterpart of BRIEF principle 5: we do not touch
  // anything outside our own scope.
  it("another hostname goes to the real WebSocket", () => {
    expect(isTunneled(new URL("wss://baska.example/ws"), "x.github.io")).toBe(false);
    expect(isTunneled(new URL("ws://localhost:9000/"), "x.github.io")).toBe(false);
  });
});

describe("localPath", () => {
  it("it strips the scope prefix", () => {
    expect(localPath(new URL("wss://x.io/PeerPages/ws?a=1"), "/PeerPages/")).toBe("/ws?a=1");
  });

  it("the scope root itself becomes /", () => {
    expect(localPath(new URL("wss://x.io/PeerPages/"), "/PeerPages/")).toBe("/");
  });

  // A root-absolute ws address (the Vite HMR `__HMR_BASE__` "/" case)
  // belongs to the tunneled page's own site.
  it("it uses a path outside the scope as it is", () => {
    expect(localPath(new URL("wss://x.io/"), "/PeerPages/")).toBe("/");
    expect(localPath(new URL("wss://x.io/socket.io/?EIO=4"), "/PeerPages/")).toBe(
      "/socket.io/?EIO=4",
    );
  });

  it("with scope / the path does not change", () => {
    expect(localPath(new URL("wss://x.io/ws?x=1"), "/")).toBe("/ws?x=1");
  });
});
