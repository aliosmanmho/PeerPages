import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";
import { startViteApp, type ViteApp } from "./fixtures/vite";

/**
 * Faz 6 kabul kriteri:
 *
 *   "Vite HMR ve Socket.IO ornegi tunel uzerinden calisiyor."
 *
 * Vite HMR'i GERCEKTEN kosuyoruz (asagidaki son test): vite zaten
 * devDependency ve HMR'in kendi el sikismasi (alt protokol `vite-hmr`,
 * token, modul guncelleme mesajlari) sahte bir ucla kanitlanamaz.
 * Socket.IO iki yeni bagimlilik getirecegi icin elle dogrulamada
 * (docs/MANUAL-TEST.md); koprunun davranisi ise test sitesinin /ws yankisi
 * ile hizli ve kirilgan olmayan sekilde olculuyor.
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

async function openTunnel(context: BrowserContext, agent: NostrAgent): Promise<Page> {
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[tunel sekmesi] ${msg.text()}`);
  });
  const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
  await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 90_000,
  });
  return page;
}

test.describe("Faz 6 — WebSocket koprusu", () => {
  test("ws echo metin ve ikili mesajlari tasiyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      extraArgs: ["--inject-ws-shim"],
    });

    try {
      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      visitor.on("console", (msg) => {
        if (msg.type() === "error") console.log(`[ziyaretci] ${msg.text()}`);
      });
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

      // Shim enjekte edilmis olmali: sayfanin kendi script'lerinden once.
      expect(await visitor.evaluate(() => "__peerpagesWsShim" in window)).toBe(true);

      const started = Date.now();
      const result = await visitor.evaluate(async () => {
        const sock = new WebSocket("ws?proto=json", "json");
        sock.binaryType = "arraybuffer";
        const opened = await new Promise<boolean>((resolve) => {
          sock.onopen = () => resolve(true);
          sock.onclose = () => resolve(false);
          setTimeout(() => resolve(false), 20_000);
        });
        if (!opened) return { opened: false };

        const next = () =>
          new Promise<MessageEvent>((resolve, reject) => {
            sock.onmessage = resolve;
            sock.onclose = (ev) => reject(new Error(`kapandi: ${ev.code}`));
            setTimeout(() => reject(new Error("mesaj gelmedi")), 20_000);
          });

        sock.send("merhaba");
        const text = (await next()).data as string;

        sock.send(new Uint8Array([1, 2, 3, 250]));
        const bin = new Uint8Array((await next()).data as ArrayBuffer);

        // 16 KiB ustu mesaj: parcali gider ve parcali gelir.
        const big = new Uint8Array(40_000);
        for (let i = 0; i < big.length; i++) big[i] = i % 256;
        sock.send(big);
        const echoed = new Uint8Array((await next()).data as ArrayBuffer);
        let same = echoed.length === big.length;
        if (same) {
          for (let i = 0; i < big.length; i++) {
            if (echoed[i] !== big[i]) {
              same = false;
              break;
            }
          }
        }

        const closeCode = await new Promise<number>((resolve) => {
          sock.onclose = (ev) => resolve(ev.code);
          sock.close(1000, "bitti");
          setTimeout(() => resolve(-1), 10_000);
        });

        return {
          opened: true,
          protocol: sock.protocol,
          text,
          bin: [...bin],
          bigOk: same,
          bigLen: echoed.length,
          closeCode,
        };
      });

      console.log(`OLCUM ws yanki turu: ${Date.now() - started} ms`);
      expect(result.opened).toBe(true);
      expect(result.protocol).toBe("json");
      expect(result.text).toBe("merhaba");
      expect(result.bin).toEqual([1, 2, 3, 250]);
      expect(result.bigLen).toBe(40_000);
      expect(result.bigOk).toBe(true);
      expect(result.closeCode).toBe(1000);

      // Olcum: kucuk mesaj gidis-donusu ve 1 MB aktarim.
      const perf = await visitor.evaluate(async () => {
        const sock = new WebSocket("ws");
        sock.binaryType = "arraybuffer";
        const ok = await new Promise<boolean>((resolve) => {
          sock.onopen = () => resolve(true);
          sock.onclose = () => resolve(false);
          setTimeout(() => resolve(false), 20_000);
        });
        if (!ok) return null;

        const next = () =>
          new Promise<MessageEvent>((resolve) => {
            sock.onmessage = resolve;
          });

        const rtts: number[] = [];
        for (let i = 0; i < 20; i++) {
          const t0 = performance.now();
          sock.send(`olcum-${i}`);
          await next();
          rtts.push(performance.now() - t0);
        }
        rtts.sort((a, b) => a - b);

        const mb = new Uint8Array(1 << 20);
        const t1 = performance.now();
        sock.send(mb);
        await next();
        const bigMs = performance.now() - t1;

        sock.close();
        return { median: rtts[10] ?? 0, bigMs };
      });
      if (perf) {
        console.log(
          `OLCUM ws kucuk mesaj RTT: medyan ${perf.median.toFixed(2)} ms (n=20); ` +
            `1 MB gidis-donus: ${perf.bigMs.toFixed(0)} ms`,
        );
      }

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  // Bayrak verilmediyse HTML'e DOKUNULMAMALI. Bu test olmadan enjeksiyonun
  // sessizce her zaman acik olmasi mumkun olurdu.
  test("bayrak verilmediyse shim enjekte edilmiyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

      expect(await visitor.evaluate(() => "__peerpagesWsShim" in window)).toBe(false);
      // HTML bayt bayt ayni: hicbir script eklenmemis.
      const html = await visitor.content();
      expect(html).not.toContain("ws-shim.js");

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  // Kopru acik ama hedefte ws ucu yok: ziyaretci anlamli bir kapanis kodu
  // gormeli (sessizce askida kalmamali).
  test("ws ucu olmayan yolda anlamli kapanis kodu", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      extraArgs: ["--inject-ws-shim"],
    });

    try {
      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

      const code = await visitor.evaluate(
        () =>
          new Promise<number>((resolve) => {
            const sock = new WebSocket("boyle-bir-uc-yok");
            sock.onclose = (ev) => resolve(ev.code);
            sock.onopen = () => resolve(-1);
            setTimeout(() => resolve(-2), 20_000);
          }),
      );
      // 1008 politika (yerel sunucu el sikismayi reddetti) ya da 1011.
      expect([1008, 1011]).toContain(code);

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });
});

test.describe("Faz 6 — SSE akis halinde", () => {
  // Faz 1'deki test `res.text()` ile TUM govdeyi bekliyordu, yani "akis
  // halinde geldi" iddiasini olcmuyordu. Burada ilk olayin akis
  // KAPANMADAN once geldigini olcuyoruz.
  test("ilk olay akis kapanmadan geliyor", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

      const result = await visitor.evaluate(
        () =>
          new Promise<{ firstAt: number; lastAt: number; events: string[] }>((resolve, reject) => {
            const started = performance.now();
            const events: string[] = [];
            let firstAt = -1;
            const es = new EventSource("sse?n=3&delay=400");
            es.onmessage = (ev) => {
              if (firstAt < 0) firstAt = performance.now() - started;
              events.push(ev.data);
              if (events.length === 3) {
                es.close();
                resolve({ firstAt, lastAt: performance.now() - started, events });
              }
            };
            es.onerror = () => {
              if (events.length < 3) {
                es.close();
                reject(new Error(`SSE hatasi, gelen: ${events.length}`));
              }
            };
            setTimeout(() => {
              es.close();
              reject(new Error(`olaylar gelmedi: ${events.length}`));
            }, 30_000);
          }),
      );

      console.log(
        `OLCUM SSE ilk olay: ${result.firstAt.toFixed(0)} ms, ucuncu: ${result.lastAt.toFixed(0)} ms`,
      );
      expect(result.events).toEqual(["olay-0", "olay-1", "olay-2"]);
      // Ilk olay, ucuncusunden belirgin sekilde ONCE gelmeli: tamponlanmis
      // bir cevap ikisini ayni anda verirdi.
      expect(result.firstAt).toBeLessThan(result.lastAt - 300);

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });
});

test.describe("Faz 6 — Vite HMR", () => {
  // Kabul kriterinin asil yarisi. Hicbir HMR ayari verilmiyor: kullanicinin
  // yapilandirmasina dokunmadan calismasi gerekiyor.
  test("kaynak degisince sayfa YENILENMEDEN guncelleniyor", async ({ context }) => {
    let app: ViteApp | undefined;
    let agent: NostrAgent | undefined;

    try {
      app = await startViteApp({ base: BASE });
      agent = await startNostrAgent({
        targetURL: app.targetURL,
        relayURLs: [relay.url],
        appURL: APP_URL,
        extraArgs: ["--inject-ws-shim"],
      });

      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      visitor.on("console", (msg) => {
        if (msg.type() === "error") console.log(`[vite sayfasi] ${msg.text()}`);
      });
      await visitor.goto(BASE);
      await expect(visitor.locator("#baslik")).toHaveText("Vite HMR fixture", {
        timeout: 60_000,
      });
      await expect(visitor.locator("#mesaj")).toHaveText("ILK-SURUM", { timeout: 60_000 });

      // HMR baglantisi kuruldu mu: tunel sekmesi acik oturumu sayiyor.
      await tunnelPage.waitForFunction(() => (window.__peerpages?.wsSessions() ?? 0) > 0, null, {
        timeout: 60_000,
      });
      await expect(tunnelPage.locator("#metrics")).toContainText("WebSocket");

      // Sayfanin yenilenmedigini kanitlamak icin bir isaret birak: yenileme
      // olursa bu kaybolur.
      await visitor.evaluate(() => {
        (window as unknown as Record<string, unknown>).__isaret = "duruyor";
      });

      const started = Date.now();
      app.setMessage("IKINCI-SURUM");

      await expect(visitor.locator("#mesaj")).toHaveText("IKINCI-SURUM", { timeout: 60_000 });
      console.log(`OLCUM HMR guncellemesi: ${Date.now() - started} ms`);

      const marker = await visitor.evaluate(
        () => (window as unknown as Record<string, unknown>).__isaret,
      );
      expect(marker).toBe("duruyor");

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent?.stop();
      app?.stop();
    }
  });
});
