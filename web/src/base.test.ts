import { describe, expect, it } from "vitest";
import { isAppShellPath } from "./base";

describe("isAppShellPath", () => {
  const base = "/PeerPages/";

  it("it recognises the app shell files", () => {
    expect(isAppShellPath("/PeerPages/connect/", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/connect", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/connect/index.html", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/sw.js", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/assets/connect-abc123.js", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/favicon.ico", base)).toBe(true);
    // Phase 6: the shim is an app shell file and must NOT go through the
    // tunnel.
    expect(isAppShellPath("/PeerPages/ws-shim.js", base)).toBe(true);
  });

  /**
   * The scope root ITSELF is not app shell: that is the local site's root
   * and with the tunnel open it goes through the tunnel (the decision in
   * BRIEF section 8).
   */
  it("it leaves the scope root and the site paths to the tunnel", () => {
    expect(isAppShellPath("/PeerPages/", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/index.html", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/api/items", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/style.css", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/logo.png", base)).toBe(false);
  });

  it("it refuses paths outside the scope", () => {
    expect(isAppShellPath("/baska/", base)).toBe(false);
    expect(isAppShellPath("/", base)).toBe(false);
    expect(isAppShellPath("/connect/", base)).toBe(false);
  });

  it("it works with the root base", () => {
    expect(isAppShellPath("/connect/", "/")).toBe(true);
    expect(isAppShellPath("/sw.js", "/")).toBe(true);
    expect(isAppShellPath("/", "/")).toBe(false);
    expect(isAppShellPath("/api/items", "/")).toBe(false);
  });
});
