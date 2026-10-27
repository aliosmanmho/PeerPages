import { expect, test } from "@playwright/test";

import {
  type Agent,
  bigFileSHA256,
  startAgent,
  startTestSite,
  type TestSite,
} from "./fixtures/agent";

/**
 * Faz 1 kabul kriterleri (BRIEF Faz 1):
 *
 *   "Ziyaretci tarayicisinda test sitesi (HTML, CSS, JS, gorsel, fetch ile
 *    JSON) github.io benzeri origin'de eksiksiz render oluyor; 5 MB dosya
 *    indirme calisiyor; sayfa yenilenince SW tunel yoksa baglanti ekranina
 *    dusuyor."
 *
 * Zincir: tarayici -> Service Worker -> tunel sayfasi -> WebRTC -> ajan ->
 * localhost test sitesi. Arada signaling sunucusu YOK; offer/answer bu test
 * tarafindan stdin/stdout uzerinden tasiniyor (sayfa offer uretir, ajan
 * answer doner).
 */

const BASE = "/PeerPages/";
// Baglanti ekrani scope kokunde DEGIL: scope koku yerel sitenin koku
// (karar ve gerekcesi BRIEF bolum 8).
const CONNECT = `${BASE}connect/`;

let site: TestSite;
let agent: Agent;

test.beforeAll(async () => {
  site = await startTestSite();
});

test.afterAll(() => {
  site?.stop();
});

// Ajan TEST BASINA yeniden baslatilir: `peerpages serve` tek bir tunel
// oturumu yurutur ve oturum kapaninca cikar, yani offer'i tek kullanimlik.
test.beforeEach(async () => {
  agent = await startAgent(site.url);
});

test.afterEach(() => {
  agent?.stop();
});

/**
 * Baglanti ekranini acar, Service Worker'i kurar ve elle signaling ile tuneli
 * acar. Donen sayfa tunel SAHIBIDIR: kapanirsa tunel kapanir.
 *
 * YON: sayfa offer uretir -> ajana stdin ile gider -> ajan answer basar ->
 * sayfaya yapistirilir.
 */
async function openTunnelPage(context: import("@playwright/test").BrowserContext) {
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[sayfa hatasi] ${msg.text()}`);
  });

  await page.goto(CONNECT);

  // Service Worker'in kurulmasini bekle. Durum METNINE degil ADIM DURUMUNA
  // bakiyoruz: metin hemen sonraki adima geciyor (ornegin "peer-id yok —
  // elle signaling yolu"), yani kararli bir sinyal degil.
  await expect(page.locator('.status__step[data-step="sw"]')).toHaveAttribute(
    "data-state",
    "done",
    { timeout: 30_000 },
  );
  // URL'de peer-id olmadigi icin elle signaling paneli acik olmali.
  await expect(page.locator("#panel-manual")).toBeVisible();

  // SAYFA offer uretir (Faz 2 rol degisimi).
  await page.locator("#btn-make-offer").click();
  await expect(page.locator("#offer-output")).not.toHaveValue("", { timeout: 40_000 });
  const offer = await page.locator("#offer-output").inputValue();
  expect(offer.length).toBeGreaterThan(20);

  // Ajan answer doner; sayfaya yapistiriyoruz.
  const answer = await agent.exchange(offer);
  await page.locator("#answer-input").fill(answer);
  await page.locator("#btn-accept-answer").click();

  await agent.waitTunnelOpen();
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 30_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 30_000,
  });

  return page;
}

test.describe("Faz 1 kabul kriterleri", () => {
  test("tunel uzerinden test sitesi eksiksiz calisiyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);

    // Tunel sayfasi acik kalmali; siteyi AYRI bir sekmede aciyoruz.
    const visitor = await context.newPage();
    const errors: string[] = [];
    visitor.on("console", (msg) => {
      if (msg.type() === "error") errors.push(msg.text());
    });

    await visitor.goto(BASE);

    await test.step("HTML render oluyor", async () => {
      await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi", {
        timeout: 30_000,
      });
    });

    await test.step("CSS uygulandi", async () => {
      // style.css tunelden gelmeden bu renk olusmaz.
      await expect(visitor.locator("body")).toHaveCSS("background-color", "rgb(13, 17, 23)");
      await expect(visitor.locator("#baslik")).toHaveCSS("color", "rgb(88, 166, 255)");
    });

    await test.step("JS calisti", async () => {
      await expect(visitor.locator("body")).toHaveAttribute("data-js-calisti", "evet");
    });

    await test.step("gorsel yuklendi", async () => {
      const natural = await visitor
        .locator("#logo")
        .evaluate((img) => (img as HTMLImageElement).naturalWidth);
      expect(natural).toBe(64);
    });

    await test.step("fetch ile JSON geldi", async () => {
      await expect(visitor.locator("body")).toHaveAttribute("data-api-calisti", "evet");
      await expect(visitor.locator("#liste .api-item")).toHaveCount(3);
      await expect(visitor.locator("#liste .api-item").first()).toHaveText("alfa = 1");
    });

    expect(errors, `sayfada konsol hatasi olmamali: ${errors.join(" | ")}`).toEqual([]);

    await visitor.close();
    await tunnelPage.close();
  });

  test("5 MB dosya indirme calisiyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    const expectedSha = bigFileSHA256();

    const result = await visitor.evaluate(async () => {
      const started = performance.now();
      const res = await fetch("big.bin");
      if (!res.ok) return { error: `HTTP ${res.status}` };
      const buf = await res.arrayBuffer();
      const elapsed = performance.now() - started;

      const digest = await crypto.subtle.digest("SHA-256", buf);
      const sha = [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");

      return { bytes: buf.byteLength, sha, elapsedMs: elapsed };
    });

    expect(result.error, `indirme basarisiz: ${result.error}`).toBeUndefined();
    expect(result.bytes).toBe(5 * 1024 * 1024);
    expect(result.sha).toBe(expectedSha);

    const mbps = 5 / ((result.elapsedMs ?? 1) / 1000);
    console.log(
      `OLCUM 5MB indirme (tarayici, SW+WebRTC): ${Math.round(result.elapsedMs ?? 0)} ms, ` +
        `${mbps.toFixed(1)} MB/s`,
    );

    await visitor.close();
    await tunnelPage.close();
  });

  test("tunel yoksa navigasyon baglanti ekranina dusuyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);

    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    // Tunel sahibi sayfayi kapat: WebRTC baglantisi sayfada tutuluyordu.
    await tunnelPage.close();

    // Yenileme: SW tunel bulamayinca baglanti ekranina yonlendirmeli.
    await visitor.reload();
    await expect(visitor.locator(".shell__title")).toHaveText("PeerPages", { timeout: 30_000 });
    await expect(visitor.locator("#panel-manual")).toBeVisible();
    expect(new URL(visitor.url()).pathname).toBe(CONNECT);

    await visitor.close();
  });

  test("tunel yoksa alt kaynak 503 doner", async ({ context }) => {
    // Once tuneli kurup SW'yi yukle, sonra tuneli kapat.
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");
    await tunnelPage.close();

    const status = await visitor.evaluate(async () => {
      const res = await fetch("api/items", { cache: "no-store" });
      return res.status;
    });
    expect(status).toBe(503);

    await visitor.close();
  });

  test("gecikme ve throughput olculuyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);

    // ctrl kanalinda ping/pong RTT.
    const rtts: number[] = [];
    for (let i = 0; i < 10; i++) {
      const rtt = await tunnelPage.evaluate(() => window.__peerpages?.ping());
      if (typeof rtt === "number") rtts.push(rtt);
    }
    const medianRtt = median(rtts);
    expect(medianRtt).toBeGreaterThan(0);

    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    // Kucuk GET gecikmesi (SW + tunel + yerel sunucu).
    const small = await visitor.evaluate(async () => {
      const samples: number[] = [];
      for (let i = 0; i < 20; i++) {
        const t0 = performance.now();
        const res = await fetch(`api/items?i=${i}`, { cache: "no-store" });
        await res.arrayBuffer();
        samples.push(performance.now() - t0);
      }
      return samples;
    });

    const sorted = [...small].sort((a, b) => a - b);
    const p95 = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))] ?? 0;

    console.log(
      `OLCUM ctrl ping RTT (tarayici): medyan ${medianRtt.toFixed(2)} ms (n=${rtts.length})`,
    );
    console.log(
      `OLCUM kucuk GET (SW+tunel): medyan ${median(small).toFixed(2)} ms, ` +
        `p95 ${p95.toFixed(2)} ms (n=${small.length})`,
    );

    await visitor.close();
    await tunnelPage.close();
  });

  test("yonlendirmede Location goreli yola cevriliyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    // /yonlendir mutlak bir Location dondurur; ajan goreli yola cevirir,
    // boylece tarayici bizim origin'imizde kalir.
    const originBefore = new URL(visitor.url()).origin;
    await visitor.goto(`${BASE}yonlendir`);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    // Yonlendirme bizim origin'imizde kaldi: ajan mutlak Location'i goreli
    // yola cevirdi, tarayici yerel sunucuya gitmeye calismadi.
    const after = new URL(visitor.url());
    expect(after.origin).toBe(originBefore);
    expect(after.pathname.startsWith(BASE)).toBe(true);

    await visitor.close();
    await tunnelPage.close();
  });

  test("SSE akis halinde geliyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    const text = await visitor.evaluate(async () => {
      const res = await fetch("sse", { cache: "no-store" });
      return res.text();
    });
    expect(text).toContain("data: olay-0");
    expect(text).toContain("data: olay-2");

    await visitor.close();
    await tunnelPage.close();
  });

  test("ajan allowlist disina istek atmiyor", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#baslik")).toHaveText("PeerPages test sitesi");

    // 1) Duz `..`: tarayici URL'yi normalize eder, istek scope DISINA cikar.
    //    SW onu yakalamaz, statik sunucu 404 doner. Yani tunele hic girmez.
    const plain = await visitor.evaluate(async () => {
      const res = await fetch("../../etc/passwd", { cache: "no-store" });
      return { status: res.status, url: res.url };
    });
    expect(plain.status).toBe(404);
    expect(new URL(plain.url).pathname.startsWith(BASE)).toBe(false);

    // 2) Yuzde kodlu bolu (`%2f`): URL ayristiricisi bunu COZMEZ, yani yol
    //    scope ICINDE kalir ve istek gercekten tunele girer. Ajan cozup
    //    `..` gorur ve reddeder (BRIEF ilke 1).
    //    Not: `%2e%2e` ise tarayici tarafinda `..`ya normalize edilip scope
    //    disina dusuyor, bu yuzden ajana hic ulasmiyor. Ajanin kodlanmis
    //    gezinmeyi reddettigi Go tarafinda dogrulaniyor
    //    (internal/proxy TestIntegrationSSRFOverTunnel).
    const encoded = await visitor.evaluate(async () => {
      const res = await fetch("..%2f..%2fetc/passwd", { cache: "no-store" });
      return { status: res.status, body: await res.text(), url: res.url };
    });
    expect(new URL(encoded.url).pathname.startsWith(BASE)).toBe(true);
    expect(encoded.status).toBe(400);
    expect(encoded.body).toContain("peerpages");

    await visitor.close();
    await tunnelPage.close();
  });
});

function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) return sorted[mid] ?? 0;
  return ((sorted[mid - 1] ?? 0) + (sorted[mid] ?? 0)) / 2;
}
