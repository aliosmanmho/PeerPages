import { describe, expect, it } from "vitest";
import { isAppShellPath } from "./base";

describe("isAppShellPath", () => {
  const base = "/PeerPages/";

  it("app shell dosyalarini tanir", () => {
    expect(isAppShellPath("/PeerPages/connect/", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/connect", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/connect/index.html", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/sw.js", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/assets/connect-abc123.js", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/favicon.ico", base)).toBe(true);
  });

  /**
   * Scope kokunun KENDISI app shell degil: orasi yerel sitenin koku ve tunel
   * acikken tunelden gecer (README bolum 8 karari).
   */
  it("scope kokunu ve site yollarini tunele birakir", () => {
    expect(isAppShellPath("/PeerPages/", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/index.html", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/api/items", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/style.css", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/logo.png", base)).toBe(false);
  });

  it("scope disindaki yollari reddeder", () => {
    expect(isAppShellPath("/baska/", base)).toBe(false);
    expect(isAppShellPath("/", base)).toBe(false);
    expect(isAppShellPath("/connect/", base)).toBe(false);
  });

  it("kok base ile calisir", () => {
    expect(isAppShellPath("/connect/", "/")).toBe(true);
    expect(isAppShellPath("/sw.js", "/")).toBe(true);
    expect(isAppShellPath("/", "/")).toBe(false);
    expect(isAppShellPath("/api/items", "/")).toBe(false);
  });
});
