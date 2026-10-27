import { describe, expect, it } from "vitest";

import { isTunneled, localPath, scopeFromSrc } from "./ws-shim";

/**
 * Shim'in saf yardimcilari. Shim'in kendisi (window.WebSocket sarmalayicisi)
 * tarayici gerektirdigi icin e2e'de dogrulaniyor (web/e2e/faz6.spec.ts);
 * burada karar veren mantik test ediliyor.
 */

describe("scopeFromSrc", () => {
  it("shim'in dizinini scope koku sayar", () => {
    expect(scopeFromSrc("https://x.github.io/PeerPages/ws-shim.js", "https://x.github.io/")).toBe(
      "/PeerPages/",
    );
  });

  it("kokte servis edildiyse /", () => {
    expect(scopeFromSrc("https://x.io/ws-shim.js", "https://x.io/")).toBe("/");
  });

  it("goreli src'yi cozer", () => {
    expect(scopeFromSrc("ws-shim.js", "https://x.io/PeerPages/sayfa")).toBe("/PeerPages/");
  });

  // Beklenmeyen bir src ile scope uydurmak, yollari yanlis kirpmak demek:
  // guvenli taraf "/".
  it("beklenmeyen dosya adinda / doner", () => {
    expect(scopeFromSrc("https://x.io/PeerPages/baska.js", "https://x.io/")).toBe("/");
    expect(scopeFromSrc(undefined, "https://x.io/")).toBe("/");
    expect(scopeFromSrc("::::", "not-a-url")).toBe("/");
  });
});

describe("isTunneled", () => {
  it("ayni hostname tunele girer", () => {
    expect(isTunneled(new URL("wss://x.github.io/ws"), "x.github.io")).toBe(true);
  });

  // Vite HMR adresine kendi portunu koyabiliyor; o port ziyaretcinin
  // tarayicisinda anlamsiz, hedefi ajandaki allowlist belirler.
  it("port farki onemsiz", () => {
    expect(isTunneled(new URL("wss://x.github.io:5173/"), "x.github.io")).toBe(true);
  });

  // README ilke 5'in shim karsiligi: kendi kapsamimizin disina dokunmuyoruz.
  it("baska hostname gercek WebSocket'e gider", () => {
    expect(isTunneled(new URL("wss://baska.example/ws"), "x.github.io")).toBe(false);
    expect(isTunneled(new URL("ws://localhost:9000/"), "x.github.io")).toBe(false);
  });
});

describe("localPath", () => {
  it("scope onekini cikarir", () => {
    expect(localPath(new URL("wss://x.io/PeerPages/ws?a=1"), "/PeerPages/")).toBe("/ws?a=1");
  });

  it("scope kokunun kendisi / olur", () => {
    expect(localPath(new URL("wss://x.io/PeerPages/"), "/PeerPages/")).toBe("/");
  });

  // Kok-mutlak ws adresi (Vite HMR'in `__HMR_BASE__` "/" durumu) tunelden
  // gecen sayfanin kendi sitesine aittir.
  it("scope disindaki yolu oldugu gibi kullanir", () => {
    expect(localPath(new URL("wss://x.io/"), "/PeerPages/")).toBe("/");
    expect(localPath(new URL("wss://x.io/socket.io/?EIO=4"), "/PeerPages/")).toBe(
      "/socket.io/?EIO=4",
    );
  });

  it("scope / ise yol degismez", () => {
    expect(localPath(new URL("wss://x.io/ws?x=1"), "/")).toBe("/ws?x=1");
  });
});
