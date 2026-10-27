import { expect, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import {
  type FakeRelay,
  type FakeTurn,
  startFakeRelay,
  startFakeTurn,
  startNostrAgent,
} from "./fixtures/nostr";

/**
 * Faz 5 kabul kriteri:
 *
 *   "Simetrik NAT simulasyonunda (or. mobil hotspot ↔ kurumsal ag) TURN ile
 *    baglaniyor."
 *
 * Simetrik NAT'i tek makinede taklit edemiyoruz. Yerine AYNI SONUCU ureten
 * durumu kuruyoruz: ajani `--ice-relay-only` ile yalnizca relay adayi
 * toplamaya zorluyoruz, yani dogrudan yol IMKANSIZ. Tunelin acilmasi, o
 * trafigin TURN'den gectiginin kanitidir — hotspot denemesinden guclu,
 * cunku orada dogrudan yol da calismis olabilir.
 *
 * Ziyaretci tarafi NORMAL yapilandirmayla geliyor: TURN kimlik bilgisi
 * sayfaya hic gitmiyor (README §8).
 *
 * Gercek iki ag denemesi elle: docs/MANUAL-TEST.md.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

let site: TestSite;
let relay: FakeRelay;

test.beforeAll(async () => {
  site = await startTestSite();
  relay = await startFakeRelay();
});

test.afterAll(() => {
  relay?.stop();
  site?.stop();
});

test.describe("Faz 5 — TURN uzerinden baglanma", () => {
  test("relay-only ajan TURN ile baglaniyor", async ({ context }) => {
    const turn: FakeTurn = await startFakeTurn();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      turn: { url: turn.url, user: turn.user, pass: turn.pass },
      iceRelayOnly: true,
    });

    try {
      const page = await context.newPage();
      page.on("console", (msg) => {
        if (msg.type() === "error") console.log(`[sayfa hatasi] ${msg.text()}`);
      });
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      const started = Date.now();
      await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);

      await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
      await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
        timeout: 90_000,
      });
      console.log(`OLCUM URL'den tunele (yalnizca TURN): ${Date.now() - started} ms`);

      // Ekran da dogruyu soylemeli: trafik bir sunucudan geciyor.
      await page.waitForFunction(() => window.__peerpages?.iceRoute() === "relay", null, {
        timeout: 30_000,
      });
      await expect(page.locator("#metrics")).toContainText("TURN uzerinden");

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
      turn.stop();
    }
  });

  // NEGATIF KONTROL: bu test olmadan yukaridaki testin dogrudan yoldan
  // gecip yanlislikla yesil kalmasi mumkun olurdu.
  test("relay-only ajan TURN olmadan baglanamiyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      // Dinlemeyen adres: allocation alinamaz, yani hic kullanilabilir
      // aday toplanmaz.
      turn: { url: "turn:127.0.0.1:1", user: "u", pass: "p" },
      iceRelayOnly: true,
    });

    try {
      const page = await context.newPage();
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);

      // Baglanti kurulmamali; ekran durumu "yeniden deneniyor"a duser
      // (Faz 4 denetleyicisi) ama tunel acilmaz.
      await page.waitForTimeout(20_000);
      expect(await page.evaluate(() => window.__peerpages?.hasTunnel())).toBe(false);
      await expect(page.locator("#panel-connected")).toBeHidden();

      await page.close();
    } finally {
      agent.stop();
    }
  });

  // TURN verilse bile dogrudan yol varsa ICE onu secer: kota bosa gitmez.
  test("TURN varken dogrudan yol tercih ediliyor", async ({ context }) => {
    const turn = await startFakeTurn();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      turn: { url: turn.url, user: turn.user, pass: turn.pass },
      // relay-only YOK: ajan hem host/srflx hem relay adayi topluyor.
    });

    try {
      const page = await context.newPage();
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);

      await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
      await page.waitForFunction(
        () => {
          const r = window.__peerpages?.iceRoute();
          return r !== undefined && r !== "bilinmiyor";
        },
        null,
        { timeout: 60_000 },
      );

      const route = await page.evaluate(() => window.__peerpages?.iceRoute());
      console.log(`OLCUM TURN verildi ama secilen yol: ${route}`);
      expect(route).not.toBe("relay");

      await page.close();
    } finally {
      agent.stop();
      turn.stop();
    }
  });
});

test.describe("Faz 5 — doctor TURN raporu", () => {
  test("doctor allocation'i dogruluyor", async () => {
    const turn = await startFakeTurn();
    try {
      const { runAgent } = await import("./fixtures/agent");
      const ok = await runAgent([
        "doctor",
        "--json",
        "--relay",
        relay.url,
        "--turn",
        turn.url,
        "--turn-user",
        turn.user,
        "--turn-pass",
        turn.pass,
      ]);
      const report = JSON.parse(ok.stdout);
      expect(report.turn.servers[0].ok).toBe(true);
      expect(report.turn.servers[0].relayed_addr).not.toBe("");
      expect(report.advice).toBeTruthy();
      console.log(`OLCUM doctor TURN allocation: ${report.turn.servers[0].ms} ms`);

      // Yanlis sifre: allocation alinamaz ve cikis kodu sifirdan farkli
      // olmali — sessizce 0 donmek, kullanicinin TURN'un calistigini
      // sanmasina yol acar.
      const bad = await runAgent([
        "doctor",
        "--json",
        "--relay",
        relay.url,
        "--turn",
        turn.url,
        "--turn-user",
        turn.user,
        "--turn-pass",
        "yanlis-sifre",
      ]);
      expect(bad.code).not.toBe(0);
      const badReport = JSON.parse(bad.stdout);
      expect(badReport.turn.servers[0].ok).toBe(false);
    } finally {
      turn.stop();
    }
  });
});
