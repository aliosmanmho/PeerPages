import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * Faz 2 kabul kriterinin otomatik karsiligi:
 *
 *   "Ziyaretci sadece URL ile baglaniyor; kopyala-yapistir yok."
 *
 * Zincir: tarayici -> Service Worker -> tunel sayfasi -> WebRTC -> ajan ->
 * localhost test sitesi. Signaling NOSTR uzerinden; ne signaling sunucusu
 * ne de elle tasima var.
 *
 * Relay varsayilan olarak SAHTE (surec ici): gercek internet relay'lerine
 * bagimli bir CI testi kirilgan olur. Gercek relay'lerle dogrulamak icin:
 *
 *   PEERPAGES_E2E_RELAYS="wss://nos.lol,wss://nostr.mom" npx playwright test nostr.spec.ts
 *
 * Iki cihaz / iki ag dogrulamasi docs/MANUAL-TEST.md'de elle yapiliyor; bunu
 * tek makineden otomatiklestirmek mumkun degil.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

/**
 * Gercek relay'lerle kosmak icin ortam degiskeni.
 *
 * Verilirse sahte relay baslatilmaz; ajan da sayfa da bu relay'leri kullanir.
 * Sahte relay'e ozgu test (ephemeral dusurme) atlanir.
 */
const REAL_RELAYS = (process.env.PEERPAGES_E2E_RELAYS ?? "")
  .split(",")
  .map((r) => r.trim())
  .filter((r) => r !== "");
const USING_REAL_RELAYS = REAL_RELAYS.length > 0;

let site: TestSite;
let relay: FakeRelay | null = null;
/** Ajanin ve sayfanin kullanacagi relay listesi. */
let relayURLs: string[] = [];

/** `?relay=` sorgu dizesi (BRIEF bolum 4: liste kullanici degistirebilir). */
function relayQuery(urls: string[] = relayURLs): string {
  return urls.map((u) => `relay=${encodeURIComponent(u)}`).join("&");
}

test.beforeAll(async () => {
  site = await startTestSite();
  if (USING_REAL_RELAYS) {
    relayURLs = REAL_RELAYS;
    console.log(`GERCEK RELAY'LER kullaniliyor: ${relayURLs.join(" ")}`);
  } else {
    relay = await startFakeRelay();
    relayURLs = [relay.url];
  }
});

test.afterAll(() => {
  relay?.stop();
  site?.stop();
});

/**
 * Ajani baslatir ve ziyaretci sayfasini URL ile acar.
 *
 * `?relay=` ziyaretcinin relay listesini degistirmesi icin (BRIEF bolum 4);
 * burada sahte relay'e yonlendirmek icin kullaniliyor, yani test icin ayri
 * bir arka kapi yok.
 */
async function openByURL(
  context: BrowserContext,
  agent: NostrAgent,
  extraQuery = "",
): Promise<Page> {
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[sayfa hatasi] ${msg.text()}`);
  });

  const query = `?${relayQuery()}${extraQuery}`;
  const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
  await page.goto(`${CONNECT}${query}${hash}`);
  return page;
}

async function expectTunnelOpen(page: Page): Promise<void> {
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 60_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 60_000,
  });
}

test.describe("Faz 2 kabul kriterleri — Nostr ile baglanma", () => {
  test("ziyaretci yalnizca URL ile baglaniyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
    });

    try {
      const started = Date.now();
      const tunnelPage = await openByURL(context, agent);

      // Kopyala-yapistir YOK: tunel kendiliginden acilmali.
      await expectTunnelOpen(tunnelPage);
      const elapsed = Date.now() - started;
      const relayKind = USING_REAL_RELAYS ? "gercek relay" : "sahte relay, loopback";
      console.log(`OLCUM URL'den tunele kadar: ${elapsed} ms (${relayKind})`);

      // peer-id ekranda kisaltilmis gosteriliyor.
      await expect(tunnelPage.locator("#nostr-peer")).toContainText(agent.npub.slice(0, 12));

      // Elle signaling paneli gorunmemeli: bu yolda gerekli degil.
      await expect(tunnelPage.locator("#panel-manual")).toBeHidden();

      // Site gercekten tunelden geliyor mu?
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi", {
        timeout: 30_000,
      });
      await expect(visitor.locator("body")).toHaveCSS("background-color", "rgb(13, 17, 23)");
      await expect(visitor.locator("body")).toHaveAttribute("data-api-calisti", "evet");
      await expect(visitor.locator("#liste .api-item")).toHaveCount(3);

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  test("5 MB dosya Nostr ile kurulan tunelden iniyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
    });

    try {
      const tunnelPage = await openByURL(context, agent);
      await expectTunnelOpen(tunnelPage);

      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

      const result = await visitor.evaluate(async () => {
        const started = performance.now();
        const res = await fetch("big.bin");
        if (!res.ok) return { error: `HTTP ${res.status}` };
        const buf = await res.arrayBuffer();
        return { bytes: buf.byteLength, elapsedMs: performance.now() - started };
      });

      expect(result.error).toBeUndefined();
      expect(result.bytes).toBe(5 * 1024 * 1024);
      const mbps = 5 / ((result.elapsedMs ?? 1) / 1000);
      console.log(
        `OLCUM 5MB indirme (Nostr yolu): ${Math.round(result.elapsedMs ?? 0)} ms, ${mbps.toFixed(1)} MB/s`,
      );

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  test("paylasilan sir ile baglaniyor", async ({ context }) => {
    const secret = "kedi-balik-7";
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
      secret,
    });

    try {
      // Ajanin bastigi URL sirri zaten icerir (#npub:secret).
      expect(agent.visitorURL).toContain(`:${secret}`);

      const tunnelPage = await openByURL(context, agent);
      await expectTunnelOpen(tunnelPage);

      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  // peer-id'yi bilip sirri bilmeyen biri BAGLANAMAZ (BRIEF bolum 4).
  test("sir gerekliyken sirsiz ziyaretci baglanamiyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
      secret: "dogru-sir",
    });

    try {
      const page = await context.newPage();
      // Sirri URL'den CIKARIYORUZ: yalnizca npub kaliyor.
      const npubOnly = agent.visitorURL.slice(agent.visitorURL.indexOf("#") + 1).split(":")[0];
      await page.goto(`${CONNECT}?${relayQuery()}#${npubOnly}`);

      // Tunel acilmamali ve kullaniciya anlamli bir hata gosterilmeli.
      await expect(page.locator("#status-msg")).toHaveAttribute("data-tone", "error", {
        timeout: 60_000,
      });
      await expect(page.locator("#panel-connected")).toBeHidden();

      await page.close();
    } finally {
      agent.stop();
    }
  });

  test("ajan calismiyorsa anlamli hata gosteriliyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
    });
    const visitorURL = agent.visitorURL;
    agent.stop(); // ajan artik yok

    const page = await context.newPage();
    const hash = visitorURL.slice(visitorURL.indexOf("#"));
    await page.goto(`${CONNECT}?${relayQuery()}${hash}`);

    // 30 sn zaman asimi sonunda "ajan yanit vermedi" gorulmeli.
    await expect(page.locator("#status-msg")).toContainText("Ajan yanit vermedi", {
      timeout: 60_000,
    });
    await expect(page.locator("#btn-retry")).toBeEnabled();

    await page.close();
  });

  test("bozuk peer-id anlamli hata veriyor", async ({ context }) => {
    const page = await context.newPage();
    await page.goto(`${CONNECT}#npub1bozukbozukbozuk`);

    await expect(page.locator("#status-msg")).toHaveAttribute("data-tone", "error", {
      timeout: 30_000,
    });
    // Elle signaling paneli de gosterilmemeli: kullanici yanlis URL'yi fark
    // etmeden elle ugrasmaya baslamasin.
    await expect(page.locator("#panel-manual")).toBeHidden();

    await page.close();
  });

  test("gecersiz relay adresi anlamli hata veriyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
    });

    try {
      const page = await context.newPage();
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      await page.goto(`${CONNECT}?relay=${encodeURIComponent("http://relay.example")}${hash}`);

      await expect(page.locator("#status-msg")).toContainText("ws://", { timeout: 30_000 });

      await page.close();
    } finally {
      agent.stop();
    }
  });

  // Ephemeral event'i dusuren relay: yayin kabul edilir ama iletilmez.
  // Ziyaretci bunu "ajan yanit vermedi" olarak gorur; `doctor` ise nedeni
  // soyler (BRIEF bolum 8).
  test("ephemeral dusuren relay ile baglanti kurulamiyor", async ({ context }) => {
    test.skip(USING_REAL_RELAYS, "bu test sahte relay'in davranisini tasiyor");
    const dropper = await startFakeRelay(["--drop-ephemeral"]);

    try {
      const agent = await startNostrAgent({
        targetURL: site.url,
        relayURLs: [dropper.url],
        appURL: APP_URL,
      });

      try {
        const page = await context.newPage();
        const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
        await page.goto(`${CONNECT}?${relayQuery([dropper.url])}${hash}`);

        await expect(page.locator("#status-msg")).toContainText("Ajan yanit vermedi", {
          timeout: 60_000,
        });
        await page.close();
      } finally {
        agent.stop();
      }
    } finally {
      dropper.stop();
    }
  });
});
