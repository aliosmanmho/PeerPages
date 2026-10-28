import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { expect, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * Produces REAL screenshots for the README (phase 7).
 *
 * SKIPPED in a normal run: `playwright.config.ts` excludes the tests
 * tagged `@demo` with `grepInvert`. Run it by hand:
 *
 *   Git Bash  : PEERPAGES_DEMO=1 npx playwright test demo
 *   PowerShell: $env:PEERPAGES_DEMO=1; npx playwright test demo
 *
 * Why it lives in a test: we want the images to be REAL. Staging a
 * screenshot is easy, but this project has refused unverifiable claims
 * from the start — these images are taken from a working tunnel.
 *
 * The 10-second GIF is NOT produced by this script: the recipe is in
 * docs/LAUNCH.md, because a recording from the user's own machine is more
 * convincing.
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

test.describe("phase 7 — the README images @demo", () => {
  // The README's hero images: on the left the connect screen the owner
  // sees, on the right the site the visitor sees.
  test("the connect screen and a site served over the tunnel", async ({ context }) => {
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

      // The visitor tab: make a few requests so the counters do not look
      // empty.
      const visitor = await context.newPage();
      // A narrow crop: the test site's content is about 470px and the
      // rest is empty space.
      await visitor.setViewportSize({ width: 1000, height: 500 });
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site");
      await visitor.evaluate(async () => {
        await fetch("api/items", { cache: "no-store" });
        // A ws session, so the "WebSocket" row shows up in the metrics
        // panel.
        const sock = new WebSocket("ws");
        await new Promise<void>((resolve) => {
          sock.onopen = () => resolve();
          setTimeout(resolve, 5000);
        });
        (window as unknown as Record<string, unknown>).__demoSocket = sock;
      });

      // Let the latency measurement and the ws row reach the panel.
      await owner.waitForFunction(() => (window.__peerpages?.wsSessions() ?? 0) > 0, null, {
        timeout: 30_000,
      });
      await expect(owner.locator("#metrics")).toContainText("WebSocket");
      await expect(owner.locator("#metrics")).toContainText("ms");

      await owner.locator("#panel-connected").screenshot({ path: join(IMG_DIR, "connect.png") });
      await visitor.screenshot({ path: join(IMG_DIR, "site.png") });

      console.log(`IMAGE ${join(IMG_DIR, "connect.png")}`);
      console.log(`IMAGE ${join(IMG_DIR, "site.png")}`);

      await visitor.close();
      await owner.close();
    } finally {
      agent.stop();
    }
  });
});
