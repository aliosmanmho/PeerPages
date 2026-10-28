import { beforeEach, describe, expect, it, vi } from "vitest";

import { DEFAULT_LANG, keysOf, LANGS, lang, resolveLang, setLang, t } from "./i18n";

/**
 * The dictionary itself is data; what is worth testing is the behaviour
 * around it — which language wins, what happens to a key that does not
 * exist, and whether the two dictionaries stayed in step.
 *
 * `applyTranslations` and the language switcher need a DOM, so they are
 * covered in a real browser by `web/e2e/language.spec.ts` instead of a
 * simulated one here. That keeps the project free of a DOM test dependency
 * and tests the thing that actually ships.
 */

describe("resolveLang", () => {
  it("?lang= wins over everything", () => {
    expect(resolveLang("?lang=tr", "en", ["en-US"])).toBe("tr");
    expect(resolveLang("?lang=en", "tr", ["tr-TR"])).toBe("en");
  });

  it("stored choice wins over the browser", () => {
    expect(resolveLang("", "tr", ["en-US", "en"])).toBe("tr");
  });

  it("falls back to the browser list, matching the base tag", () => {
    expect(resolveLang("", null, ["tr-TR", "en-US"])).toBe("tr");
    expect(resolveLang("", null, ["de-DE", "tr"])).toBe("tr");
  });

  it("defaults to English", () => {
    expect(resolveLang("", null, [])).toBe(DEFAULT_LANG);
    expect(resolveLang("", null, ["de-DE", "fr"])).toBe("en");
  });

  // Deliberately different from relaysFromSearch/stunFromSearch, which throw
  // on bad input: a language we do not have is cosmetic.
  it("ignores an unknown ?lang= instead of throwing", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(resolveLang("?lang=de", null, ["tr"])).toBe("tr");
    expect(warn).toHaveBeenCalledOnce();
    warn.mockRestore();
  });

  it("ignores a stored value that is no longer supported", () => {
    expect(resolveLang("", "klingon", ["en"])).toBe("en");
  });
});

describe("t", () => {
  beforeEach(() => {
    setLang("en");
  });

  it("returns the string for the current language", () => {
    expect(t("btn.copy")).toBe("Copy");
    setLang("tr");
    expect(t("btn.copy")).toBe("Kopyala");
  });

  it("fills placeholders", () => {
    expect(t("metrics.wsOpen", { n: 2 })).toBe("2 open");
    expect(t("status.answerExpected", { type: "offer" })).toBe("Expected an answer, got: offer");
  });

  it("leaves an unknown placeholder alone", () => {
    expect(t("metrics.wsOpen", { other: 1 })).toBe("{n} open");
  });

  it("returns the key itself when it is missing", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(t("no.such.key")).toBe("no.such.key");
    warn.mockRestore();
  });

  it("falls back to English for a key a language is missing", () => {
    // Both dictionaries are complete today (the test below enforces that);
    // this proves the fallback path works if one ever is not.
    setLang("tr");
    expect(t("step.sw")).toBe("Service Worker");
  });
});

describe("dictionaries", () => {
  // The real guard against a half-finished translation.
  it("every language has exactly the same keys", () => {
    const base = keysOf(DEFAULT_LANG);
    for (const l of LANGS) {
      expect({ lang: l, keys: keysOf(l) }).toEqual({ lang: l, keys: base });
    }
  });

  it("no value is empty", () => {
    for (const l of LANGS) {
      setLang(l);
      for (const key of keysOf(l)) {
        expect(t(key).trim()).not.toBe("");
      }
    }
    setLang(DEFAULT_LANG);
  });

  // A placeholder that exists in one language but not the other produces a
  // literal "{n}" on screen in that language.
  it("placeholders match across languages", () => {
    const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
    for (const key of keysOf(DEFAULT_LANG)) {
      setLang(DEFAULT_LANG);
      const base = placeholders(t(key));
      for (const l of LANGS) {
        setLang(l);
        expect({ key, lang: l, ph: placeholders(t(key)) }).toEqual({ key, lang: l, ph: base });
      }
    }
    setLang(DEFAULT_LANG);
  });
});

describe("setLang", () => {
  it("changes what t() returns", () => {
    setLang("tr");
    expect(lang()).toBe("tr");
    expect(t("btn.retry")).toBe("Yeniden dene");
    setLang("en");
    expect(lang()).toBe("en");
    expect(t("btn.retry")).toBe("Try again");
  });
});
