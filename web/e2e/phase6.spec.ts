import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";
import { startViteApp, type ViteApp } from "./fixtures/vite";

/**
 * The phase 6 acceptance criterion:
 *
 *   "The Vite HMR and Socket.IO examples work over the tunnel."
 *
 * We REALLY run Vite HMR (the last test below): vite is already a
 * devDependency, and HMR's own handshake (the `vite-hmr` subprotocol, the
 * token, the module update messages) cannot be proven with a fake
 * endpoint. Socket.IO would bring two new dependencies, so it sits in the
 * manual verification (docs/MANUAL-TEST.md); the bridge's own behaviour
 * is measured fast and without fragility against the test site's /ws
 * echo.
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
    if (msg.type() === "error") console.log(`[tunnel tab] ${msg.text()}`);
  });
  const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
  await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 90_000,
  });
  return page;
}

test.describe("phase 6 — the WebSocket bridge", () => {
  test("the ws echo carries text and binary messages", async ({ context }) => {
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
        if (msg.type() === "error") console.log(`[visitor] ${msg.text()}`);
      });
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

      // The shim has to have been injected, before the page's own
      // scripts.
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

        // A message over 16 KiB: it goes out in chunks and comes back in
        // chunks.
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

      console.log(`MEASURED ws echo round trip: ${Date.now() - started} ms`);
      expect(result.opened).toBe(true);
      expect(result.protocol).toBe("json");
      expect(result.text).toBe("merhaba");
      expect(result.bin).toEqual([1, 2, 3, 250]);
      expect(result.bigLen).toBe(40_000);
      expect(result.bigOk).toBe(true);
      expect(result.closeCode).toBe(1000);

      // The measurement: a small message round trip and a 1 MB
      // transfer.
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
          sock.send(`measure-${i}`);
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
          `MEASURED ws small message RTT: median ${perf.median.toFixed(2)} ms (n=20); ` +
            `1 MB gidis-donus: ${perf.bigMs.toFixed(0)} ms`,
        );
      }

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  // Without the flag the HTML must NOT BE TOUCHED. Without this test the
  // injection could be silently always on.
  test("without the flag no shim is injected", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

      expect(await visitor.evaluate(() => "__peerpagesWsShim" in window)).toBe(false);
      // The HTML is byte-for-byte the same: no script was added.
      const html = await visitor.content();
      expect(html).not.toContain("ws-shim.js");

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  // The bridge is on but the target has no ws endpoint: the visitor has
  // to see a meaningful close code (rather than hanging silently).
  test("a meaningful close code on a path with no ws endpoint", async ({ context }) => {
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
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

      const code = await visitor.evaluate(
        () =>
          new Promise<number>((resolve) => {
            const sock = new WebSocket("boyle-bir-uc-yok");
            sock.onclose = (ev) => resolve(ev.code);
            sock.onopen = () => resolve(-1);
            setTimeout(() => resolve(-2), 20_000);
          }),
      );
      // 1008 policy (the local server refused the handshake) or 1011.
      expect([1008, 1011]).toContain(code);

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });
});

test.describe("phase 6 — SSE as a stream", () => {
  // The phase 1 test awaited the WHOLE body with `res.text()`, so it did
  // not measure the "it arrived as a stream" claim. Here we measure that
  // the first event arrives BEFORE the stream closes.
  test("the first event arrives before the stream closes", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const tunnelPage = await openTunnel(context, agent);
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

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
        `MEASURED SSE first event: ${result.firstAt.toFixed(0)} ms, third: ${result.lastAt.toFixed(0)} ms`,
      );
      expect(result.events).toEqual(["event-0", "event-1", "event-2"]);
      // The first event has to arrive clearly BEFORE the third: a
      // buffered response would give both at once.
      expect(result.firstAt).toBeLessThan(result.lastAt - 300);

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });
});

test.describe("phase 6 — Vite HMR", () => {
  // The real half of the acceptance criterion. No HMR setting is passed:
  // it has to work without touching the user's configuration.
  test("when a source changes the page updates WITHOUT RELOADING", async ({ context }) => {
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
      await expect(visitor.locator("#title")).toHaveText("Vite HMR fixture", {
        timeout: 60_000,
      });
      await expect(visitor.locator("#message")).toHaveText("FIRST-VERSION", { timeout: 60_000 });

      // Did the HMR connection come up: the tunnel tab counts the open
      // session.
      await tunnelPage.waitForFunction(() => (window.__peerpages?.wsSessions() ?? 0) > 0, null, {
        timeout: 60_000,
      });
      await expect(tunnelPage.locator("#metrics")).toContainText("WebSocket");

      // Leave a mark to prove the page did not reload: a reload would
      // lose it.
      await visitor.evaluate(() => {
        (window as unknown as Record<string, unknown>).__marker = "still-here";
      });

      const started = Date.now();
      app.setMessage("SECOND-VERSION");

      await expect(visitor.locator("#message")).toHaveText("SECOND-VERSION", { timeout: 60_000 });
      console.log(`MEASURED HMR update: ${Date.now() - started} ms`);

      const marker = await visitor.evaluate(
        () => (window as unknown as Record<string, unknown>).__marker,
      );
      expect(marker).toBe("still-here");

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent?.stop();
      app?.stop();
    }
  });
});
