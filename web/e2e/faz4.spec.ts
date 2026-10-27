import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * Faz 4 kabul kriterinin otomatiklestirilebilir cekirdegi:
 *
 *   "Laptop uyuyup uyaninca tunel kendini toparliyor."
 *
 * Gercek uyku/uyanma tek makineden ve tarayici icinden taklit edilemez;
 * yerine ayni SONUCU ureten durumu kuruyoruz: ajan olup ayni anahtarla
 * geri geliyor, ag kesilip geri geliyor. Elle yapilacak gercek uyku
 * denemesi docs/MANUAL-TEST.md'de.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

let site: TestSite;

test.beforeAll(async () => {
  site = await startTestSite();
});

test.afterAll(() => {
  site?.stop();
});

async function openVisitorTab(
  context: BrowserContext,
  agent: NostrAgent,
  relay: FakeRelay,
): Promise<Page> {
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[sayfa hatasi] ${msg.text()}`);
  });
  const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
  await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);
  return page;
}

async function expectTunnelOpen(page: Page): Promise<void> {
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 60_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 60_000,
  });
}

test.describe("Faz 4 — tunel kendini toparliyor", () => {
  // Ajan olup ayni anahtarla geri geldiginde ziyaretcinin hicbir sey
  // yapmasi gerekmemeli. Faz 3'e kadar sayfa tuneli birakip ELLE SIGNALING
  // panelini aciyordu; URL ile gelen ziyaretci icin tek cikis yenilemekti.
  test("ajan yeniden baslayinca sayfa kendi kendine baglaniyor", async ({ context }) => {
    const relay = await startFakeRelay();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(page);
      expect(await page.evaluate(() => window.__peerpages?.reconnects() ?? -1)).toBe(0);

      const started = Date.now();
      await agent.restart();

      // Elle tiklama YOK: denetleyici kendi geri sayimiyla toparlamali.
      await page.waitForFunction(() => (window.__peerpages?.reconnects() ?? 0) > 0, null, {
        timeout: 90_000,
      });
      await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
        timeout: 90_000,
      });
      console.log(`OLCUM yeniden baglanma (ajan yeniden basladi): ${Date.now() - started} ms`);

      expect(await page.evaluate(() => window.__peerpages?.connState())).toBe("connected");

      // Tunel gercekten calisiyor: ziyaretci sekmesi siteyi goruyor.
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi", {
        timeout: 30_000,
      });
      await visitor.close();
      await page.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });

  // Kopma aninda elle signaling paneli ACILMAMALI ve ekran ne oldugunu
  // soylemeli: eski davranis "Yeniden offer uretebilirsin" diyordu.
  test("kopmada ekran yeniden baglandigini soyluyor", async ({ context }) => {
    const relay = await startFakeRelay();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(page);

      // Ajani oldur ve GERI GETIRME: sayfa denemeye devam etmeli.
      agent.process.kill();

      await expect(page.locator("#status-msg")).toContainText("yeniden", { timeout: 90_000 });
      await expect(page.locator("#panel-manual")).toBeHidden();
      await expect(page.locator("#panel-connected")).toBeVisible();
      expect(await page.evaluate(() => window.__peerpages?.connState())).toBe("reconnecting");

      await page.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });
});

test.describe("Faz 4 — relay dayanikliligi", () => {
  // Relay dusup geri geldiginde ajan surecinden CIKMAMALI ve yeniden
  // abone olmali. Faz 3'e kadar abonelik kanali kapandiginda Run hata
  // donuyor, ajan exit 1 ile oluyordu.
  test("relay dusup geri gelince ajan hala yayinda", async ({ context }) => {
    let relay = await startFakeRelay();
    const port = relay.port;

    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      // Relay'i oldur, kisa bir sure bekle, AYNI portta geri getir.
      relay.stop();
      await new Promise((r) => setTimeout(r, 1500));
      relay = await startFakeRelay({ port });

      // Ajan olmemis olmali.
      expect(agent.process.exitCode).toBeNull();

      // Ve yeni bir ziyaretci hala baglanabilmeli: abonelik yenilenmis
      // olmali, yoksa offer'i kimse duymaz.
      const page = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(page);
      await page.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });
});

test.describe("Faz 4 — SW bekleme penceresi", () => {
  // Kopma aninda istenen bir alt kaynak aninda 503 almak yerine yeniden
  // baglanmayi beklemeli: ziyaretcinin gezdigi site kirilmasin.
  test("kopma aninda alt kaynak 503 yerine tamamlaniyor", async ({ context }) => {
    const relay = await startFakeRelay();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const owner = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(owner);

      // SW'nin devrede oldugundan emin ol.
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi", {
        timeout: 30_000,
      });

      // Ajani yeniden baslat; bu arada ziyaretci sekmesinden istek yap.
      const restarted = agent.restart();
      const status = await visitor.evaluate(async (base) => {
        // Kopmanin fark edilmesi icin kisa bir an bekle: istek tam
        // boslukta gitsin.
        await new Promise((r) => setTimeout(r, 1500));
        const res = await fetch(`${base}api/items`, { cache: "no-store" });
        return res.status;
      }, BASE);
      await restarted;

      // 503 da gorulebilir (bekleme penceresi dolarsa) ama bu testin
      // olcmek istedigi sey tam olarak bunun OLMAMASI.
      expect(status).toBe(200);

      await visitor.close();
      await owner.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });
});
