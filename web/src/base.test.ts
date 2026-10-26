import { describe, expect, it } from "vitest";
import { isAppShellPath } from "./base";

describe("isAppShellPath", () => {
  const base = "/PeerPages/";

  it("app shell dosyalarini tanir", () => {
    expect(isAppShellPath("/PeerPages/", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/index.html", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/sw.js", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/assets/main-abc123.js", base)).toBe(true);
    expect(isAppShellPath("/PeerPages/favicon.ico", base)).toBe(true);
  });

  it("tunele gidecek yollari app shell saymaz", () => {
    expect(isAppShellPath("/PeerPages/api/items", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/style.css", base)).toBe(false);
    expect(isAppShellPath("/PeerPages/logo.png", base)).toBe(false);
  });

  it("scope disindaki yollari reddeder", () => {
    expect(isAppShellPath("/baska/", base)).toBe(false);
    expect(isAppShellPath("/", base)).toBe(false);
  });

  it("kok base ile calisir", () => {
    expect(isAppShellPath("/", "/")).toBe(true);
    expect(isAppShellPath("/sw.js", "/")).toBe(true);
    expect(isAppShellPath("/api/items", "/")).toBe(false);
  });
});
