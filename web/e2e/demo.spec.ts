import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { expect, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * README icin GERCEK ekran goruntuleri uretir (Faz 7).
 *
 * Normal kosumda ATLANIR: `playwright.config.ts` icinde `grepInvert` ile
 * `@demo` etiketli testler dislaniyor. Elle kostur:
 *
 *   Git Bash : PEERPAGES_DEMO=1 npx playwright test demo
 *   PowerShell: $env:PEERPAGES_DEMO=1; npx playwright test demo
 *
 * Neden test icinde: goruntunun GERCEK olmasini istiyoruz. Kurgu bir
 * ekran goruntusu hazirlamak kolay ama bu proje, dogrulanamayan iddialari
 * baslangictan beri reddediyor -- bu goruntuler calisan bir tunelden
 * aliniyor.
 *
 * 10 saniyelik GIF bu betikle UReTILMIYOR: tarifi docs/LAUNCH.md'de,
 * cunku kullanicinin kendi makinesinin kaydi daha ikna edici.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

const IMG_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "docs", "img");

let site: TestSite;
let relay: FakeRelay;

test.beforeAll(async () => {
  mkdirSync(IMG_DIR, { recursive: true });
  site = await startTestSite();
  relay = await startFakeRelay();
});

test.afterAll(() => {
  relay?.stop();
  site?.stop();
});

test.describe("Faz 7 — README gorselleri @demo", () => {
  // README'nin hero gorselleri: solda sahibin gordugu baglanti ekrani,
  // sagda ziyaretcinin gordugu site.
  test("baglanti ekrani ve tunelden gecen site", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      extraArgs: ["--inject-ws-shim"],
    });

    try {
      const owner = await context.newPage();
      await owner.setViewportSize({ width: 1000, height: 820 });
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      await owner.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);
      await expect(owner.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
      await owner.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
        timeout: 90_000,
      });

      // Ziyaretci sekmesi: birkac istek yap ki sayaclar bos gorunmesin.
      const visitor = await context.newPage();
      // Dar bir kirpma: test sitesinin icerigi ~470px, gerisi bos alan.
      await visitor.setViewportSize({ width: 1000, height: 500 });
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");
      await visitor.evaluate(async () => {
        await fetch("api/items", { cache: "no-store" });
        // ws oturumu: metrik panelindeki "WebSocket" satiri gorunsun.
        const sock = new WebSocket("ws");
        await new Promise<void>((resolve) => {
          sock.onopen = () => resolve();
          setTimeout(resolve, 5000);
        });
        (window as unknown as Record<string, unknown>).__demoSocket = sock;
      });

      // Gecikme olcumu ve ws satiri panele dussun.
      await owner.waitForFunction(() => (window.__peerpages?.wsSessions() ?? 0) > 0, null, {
        timeout: 30_000,
      });
      await expect(owner.locator("#metrics")).toContainText("WebSocket");
      await expect(owner.locator("#metrics")).toContainText("ms");

      await owner.locator("#panel-connected").screenshot({ path: join(IMG_DIR, "connect.png") });
      await visitor.screenshot({ path: join(IMG_DIR, "site.png") });

      console.log(`GORSEL ${join(IMG_DIR, "connect.png")}`);
      console.log(`GORSEL ${join(IMG_DIR, "site.png")}`);

      await visitor.close();
      await owner.close();
    } finally {
      agent.stop();
    }
  });
});
