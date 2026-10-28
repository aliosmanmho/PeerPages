import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * The automatable core of the phase 4 acceptance criterion:
 *
 *   "When the laptop sleeps and wakes, the tunnel recovers on its own."
 *
 * A real sleep/wake cannot be imitated from a single machine inside a
 * browser; instead we build the situation that produces the same RESULT:
 * the agent dies and comes back with the same key, the network drops and
 * comes back. The real sleep attempt, done by hand, is in
 * docs/MANUAL-TEST.md.
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
    if (msg.type() === "error") console.log(`[page error] ${msg.text()}`);
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

test.describe("phase 4 — the tunnel recovers on its own", () => {
  // When the agent dies and comes back with the same key, the visitor
  // should have to do nothing. Until phase 3 the page gave up the tunnel
  // and opened the MANUAL SIGNALING panel; for a visitor who arrived by
  // URL the only way out was a refresh.
  test("when the agent restarts the page reconnects on its own", async ({ context }) => {
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

      // NO manual clicking: the supervisor has to recover on its own
      // backoff.
      await page.waitForFunction(() => (window.__peerpages?.reconnects() ?? 0) > 0, null, {
        timeout: 90_000,
      });
      await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
        timeout: 90_000,
      });
      console.log(`MEASURED reconnect (the agent restarted): ${Date.now() - started} ms`);

      expect(await page.evaluate(() => window.__peerpages?.connState())).toBe("connected");

      // The tunnel really works: the visitor tab sees the site.
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site", {
        timeout: 30_000,
      });
      await visitor.close();
      await page.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });

  // On a drop the manual signaling panel must NOT open, and the screen
  // has to say what happened: the old behaviour said "you can create a
  // new offer".
  test("on a drop the screen says it is reconnecting", async ({ context }) => {
    const relay = await startFakeRelay();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(page);

      // Kill the agent and DO NOT bring it back: the page has to keep
      // trying.
      agent.process.kill();

      await expect(page.locator("#status-msg")).toContainText("retrying", { timeout: 90_000 });
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

test.describe("phase 4 — relay resilience", () => {
  // When a relay goes down and comes back the agent process must NOT
  // EXIT, and it has to resubscribe. Until phase 3 Run returned an error
  // when the subscription channel closed and the agent died with exit
  // 1.
  test("when a relay goes down and comes back the agent is still live", async ({ context }) => {
    let relay = await startFakeRelay();
    const port = relay.port;

    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      // Kill the relay, wait a moment, and bring it back on the SAME
      // port.
      relay.stop();
      await new Promise((r) => setTimeout(r, 1500));
      relay = await startFakeRelay({ port });

      // The agent must not have died.
      expect(agent.process.exitCode).toBeNull();

      // And a new visitor still has to be able to connect: the
      // subscription must have been renewed, or nobody hears the
      // offer.
      const page = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(page);
      await page.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });
});

test.describe("phase 4 — the SW waiting window", () => {
  // A subresource requested during a drop has to wait for the reconnect
  // instead of getting an immediate 503, so the site the visitor is
  // browsing does not break.
  test("a subresource caught by a drop completes instead of 503", async ({ context }) => {
    const relay = await startFakeRelay();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const owner = await openVisitorTab(context, agent, relay);
      await expectTunnelOpen(owner);

      // Make sure the SW is in control.
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site", {
        timeout: 30_000,
      });

      // Restart the agent, and make a request from the visitor tab
      // meanwhile.
      const restarted = agent.restart();
      const status = await visitor.evaluate(async (base) => {
        // Wait a moment so the drop gets noticed: the request should go
        // out right in the gap.
        await new Promise((r) => setTimeout(r, 1500));
        const res = await fetch(`${base}api/items`, { cache: "no-store" });
        return res.status;
      }, BASE);
      await restarted;

      // A 503 can happen too (if the waiting window expires), but what
      // this test measures is exactly that it does NOT.
      expect(status).toBe(200);

      await visitor.close();
      await owner.close();
    } finally {
      agent.stop();
      relay.stop();
    }
  });
});
