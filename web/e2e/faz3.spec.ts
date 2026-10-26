import { createServer, type Server } from "node:http";
import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * Faz 3 kabul kriterinin otomatik karsiligi:
 *
 *   "Tek ajan iki projeyi yayinliyor; ekranda anlamli durum bilgisi var."
 *
 * Ikinci hedef icin kucuk bir node sunucusu kullaniyoruz: iki kopya ayni
 * test sitesini sunsa "her ziyaretci KENDI sitesini goruyor" iddiasi
 * dogrulanamazdi — cevaplarin birbirinden ayirt edilebilmesi gerekiyor.
 *
 * Iki cihaz / iki ag dogrulamasi (ve QR'in gercekten okunabildigi)
 * docs/MANUAL-TEST.md'de elle yapiliyor; tek makineden otomatiklestirilemez.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

const ADMIN_MARKER = "YONETIM PANELI";

let site: TestSite;
let relay: FakeRelay;
let admin: { url: string; stop: () => void };

/** Ikinci hedef: kendini tanitan minik bir sunucu. */
async function startAdminSite(): Promise<{ url: string; stop: () => void }> {
  const server: Server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(`<!doctype html><html><body><h1 id="baslik">${ADMIN_MARKER}</h1></body></html>`);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address();
  if (addr === null || typeof addr === "string") throw new Error("admin sitesi adresi yok");
  return {
    url: `http://127.0.0.1:${addr.port}`,
    stop: () => server.close(),
  };
}

test.beforeAll(async () => {
  site = await startTestSite();
  admin = await startAdminSite();
  relay = await startFakeRelay();
});

test.afterAll(() => {
  relay?.stop();
  admin?.stop();
  site?.stop();
});

function relayQuery(): string {
  return `relay=${encodeURIComponent(relay.url)}`;
}

/** Ajanin bastigi URL'yi (hedefiyle birlikte) acar. */
async function openByURL(
  context: BrowserContext,
  agent: NostrAgent,
  targetName: string,
): Promise<Page> {
  const printed = agent.urls[targetName];
  if (!printed) {
    throw new Error(`ajan '${targetName}' icin URL basmadi: ${Object.keys(agent.urls).join(", ")}`);
  }
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[sayfa hatasi] ${msg.text()}`);
  });
  const hash = printed.slice(printed.indexOf("#"));
  await page.goto(`${CONNECT}?${relayQuery()}${hash}`);
  return page;
}

async function expectTunnelOpen(page: Page): Promise<void> {
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 60_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 60_000,
  });
}

/** Iki hedefle ajan baslatir. */
function startTwoTargetAgent(): Promise<NostrAgent> {
  return startNostrAgent({
    targetURL: site.url,
    extraTargets: { admin: admin.url },
    relayURLs: [relay.url],
    appURL: APP_URL,
  });
}

test.describe("Faz 3 kabul kriterleri — coklu hedef", () => {
  test("tek ajan iki projeyi yayinliyor", async ({ context }) => {
    const agent = await startTwoTargetAgent();

    try {
      // Ajan her hedef icin ayri URL basmali.
      expect(Object.keys(agent.urls).sort()).toEqual(["admin", "default"]);
      expect(agent.urls.admin).toContain(`#${agent.npub}/admin`);
      expect(agent.urls.default).toContain(`#${agent.npub}`);
      expect(agent.urls.default).not.toContain("/admin");

      // 1) Varsayilan hedef: test sitesi.
      const defaultTab = await openByURL(context, agent, "default");
      await expectTunnelOpen(defaultTab);

      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi", {
        timeout: 30_000,
      });
      await visitor.close();

      // Tunel sahibi sekmeyi kapat: ikinci hedef icin temiz baslayalim.
      await defaultTab.close();

      // 2) admin hedefi: AYNI ajan, BASKA proje.
      const adminTab = await openByURL(context, agent, "admin");
      await expectTunnelOpen(adminTab);
      await expect(adminTab.locator("#nostr-peer")).toContainText("/ admin");

      const adminVisitor = await context.newPage();
      await adminVisitor.goto(BASE);
      await expect(adminVisitor.locator("#baslik")).toHaveText(ADMIN_MARKER, { timeout: 30_000 });
      await adminVisitor.close();
      await adminTab.close();
    } finally {
      agent.stop();
    }
  });

  // Sessizce default'a dusmek yok: ziyaretci yanlis projeyi gezdigini fark
  // etmeyebilir.
  test("bilinmeyen hedef anlamli hata veriyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await context.newPage();
      const printed = agent.visitorURL;
      const hash = printed.slice(printed.indexOf("#"));
      await page.goto(`${CONNECT}?${relayQuery()}${hash}/yokboylehedef`);

      await expect(page.locator("#status-msg")).toContainText("yokboylehedef", {
        timeout: 60_000,
      });
      await expect(page.locator("#panel-connected")).toBeHidden();
      await page.close();
    } finally {
      agent.stop();
    }
  });

  test("gecersiz hedef adi URL'de yakalaniyor", async ({ context }) => {
    // Gecerli bir npub gerekiyor: peer-id kontrolu hedef kontrolunden ONCE
    // geliyor, yoksa test hedefi degil checksum hatasini olcer. Ajani
    // yalnizca npub icin baslatip hemen kapatiyoruz — bu kontrol tamamen
    // ISTEMCI tarafinda, ajana hic mesaj gitmemeli.
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });
    const npub = agent.npub;
    agent.stop();

    const page = await context.newPage();
    await page.goto(`${CONNECT}?${relayQuery()}#${npub}/-gecersiz`);
    await expect(page.locator("#status-msg")).toContainText("hedef adi gecersiz", {
      timeout: 30_000,
    });
    await expect(page.locator("#panel-connected")).toBeHidden();
    await page.close();
  });
});

test.describe("Faz 3 — baglanti ekrani", () => {
  test("durum bilgisi, kopyala butonu ve QR gosteriliyor", async ({ context }) => {
    const agent = await startTwoTargetAgent();

    try {
      const page = await openByURL(context, agent, "admin");
      await expectTunnelOpen(page);

      // Ölçümler: gecikme olculuyor, hedef listesi hello'dan geliyor.
      const metrics = page.locator("#metrics");
      await expect(metrics).toContainText("Gecikme");
      await expect(metrics).toContainText("ms", { timeout: 30_000 });
      await expect(metrics).toContainText("admin, default");
      await expect(metrics).toContainText("Aktif hedef");

      // Paylasim bloku: adres ve iki buton.
      await expect(page.locator("#share-url")).toContainText(agent.npub.slice(0, 12));
      await expect(page.locator("#btn-copy-url")).toBeVisible();

      // QR paneli kapali baslar, butonla acilir ve canvas gercekten cizilir.
      await expect(page.locator("#qr")).toBeHidden();
      await page.locator("#btn-qr").click();
      await expect(page.locator("#qr")).toBeVisible();

      const drawn = await page.evaluate(() => {
        const canvas = document.getElementById("qr-canvas") as HTMLCanvasElement | null;
        if (!canvas || canvas.width === 0) return null;
        const ctx = canvas.getContext("2d");
        if (!ctx) return null;
        const { data } = ctx.getImageData(0, 0, canvas.width, canvas.height);
        let dark = 0;
        for (let i = 0; i < data.length; i += 4) {
          if ((data[i] ?? 255) < 128) dark++;
        }
        return { size: canvas.width, darkRatio: dark / (data.length / 4) };
      });

      expect(drawn).not.toBeNull();
      // Bos ya da tamamen siyah bir canvas'i da yakalayalim: QR'in koyu
      // modul orani makul bir araliktadir.
      expect(drawn?.darkRatio ?? 0).toBeGreaterThan(0.2);
      expect(drawn?.darkRatio ?? 1).toBeLessThan(0.6);
      console.log(
        `OLCUM QR canvas: ${drawn?.size} px, koyu oran ${(drawn?.darkRatio ?? 0).toFixed(3)}`,
      );

      await page.close();
    } finally {
      agent.stop();
    }
  });

  test("istek sayaclari artiyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await openByURL(context, agent, "default");
      await expectTunnelOpen(page);

      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi", {
        timeout: 30_000,
      });
      await visitor.close();

      const stats = await page.evaluate(() => window.__peerpages?.stats());
      expect(stats?.requests ?? 0).toBeGreaterThan(0);
      expect(stats?.tunnel?.bytesIn ?? 0).toBeGreaterThan(0);

      // Ekranda da gorunmeli.
      await expect(page.locator("#metrics")).toContainText("Istek");
      await page.close();
    } finally {
      agent.stop();
    }
  });
});

test.describe("Faz 3 — ziyaretci gidince kaynak birakiliyor", () => {
  // Ajan tarafi bosta kalma denetimi: sekme kapandiginda (ya da haber
  // ulasmadiginda) oturum kendi saatiyle kapanir. Burada kisa sureler
  // veriyoruz, yoksa test 30 saniye beklemek zorunda kalir.
  test("sahip sekme kapaninca oturum kapaniyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await openByURL(context, agent, "default");
      await expectTunnelOpen(page);

      // Sayfa kapanirken `bye` yolluyor; ajan oturumu birakmali.
      await page.close();

      await expect
        .poll(
          () =>
            agent.output().some((l) => l.includes("tunel kapandi") || l.includes("oturum bosta")),
          { timeout: 60_000, message: "ajan oturumu birakmadi" },
        )
        .toBe(true);
    } finally {
      agent.stop();
    }
  });
});
