import { expect, test } from "@playwright/test";

/**
 * Phase 8: the connect screen speaks English by default and Turkish on
 * request.
 *
 * No agent and no tunnel here: what is being measured is the language
 * resolution and the picker. The unit tests cover the resolution order
 * itself (src/i18n.test.ts); this spec checks the part that only a real
 * browser can show — that the DOM really is filled, that `<html lang>`
 * follows, and that the choice survives a reload through localStorage.
 */

const CONNECT = "/PeerPages/connect/";

/** The language select in the header. */
const PICKER = "#lang-select";

test.describe("phase 8 — the page speaks two languages", () => {
  test("English by default", async ({ page }) => {
    await page.goto(CONNECT);

    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator(".shell__sub")).toHaveText("Serverless tunnel");
    await expect(page.locator(PICKER)).toHaveValue("en");

    // With no peer-id in the URL the intro block and the manual flow are
    // the two things the visitor sees, so they are the ones worth
    // asserting.
    await expect(page.locator("#panel-intro .panel__title")).toHaveText("What is this?");
    await expect(page.locator("#panel-manual .panel__title").first()).toHaveText(
      "1. Create an offer and paste it into the agent",
    );
  });

  test("?lang=tr switches the whole screen to Turkish", async ({ page }) => {
    await page.goto(`${CONNECT}?lang=tr`);

    await expect(page.locator("html")).toHaveAttribute("lang", "tr");
    await expect(page.locator(".shell__sub")).toHaveText("Sunucusuz tünel");
    await expect(page.locator(PICKER)).toHaveValue("tr");
    await expect(page.locator("#panel-intro .panel__title")).toHaveText("Bu nedir?");
  });

  test("an unknown ?lang= falls back to English instead of breaking", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));

    await page.goto(`${CONNECT}?lang=klingon`);

    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator(".shell__sub")).toHaveText("Serverless tunnel");
    expect(errors).toEqual([]);
  });

  test("the picker survives a reload", async ({ page }) => {
    await page.goto(CONNECT);
    await page.locator(PICKER).selectOption("tr");

    await expect(page.locator("html")).toHaveAttribute("lang", "tr");
    await expect(page.locator(".shell__sub")).toHaveText("Sunucusuz tünel");

    // The choice is kept in localStorage, so a reload with no query
    // string has to come back in Turkish.
    await page.reload();

    await expect(page.locator("html")).toHaveAttribute("lang", "tr");
    await expect(page.locator(PICKER)).toHaveValue("tr");

    // And `?lang=` still wins over the stored choice.
    await page.goto(`${CONNECT}?lang=en`);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
  });
});
