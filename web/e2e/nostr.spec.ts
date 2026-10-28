import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * The automated counterpart of the phase 2 acceptance criterion:
 *
 *   "The visitor connects with the URL alone; no copy-paste."
 *
 * The chain: browser -> Service Worker -> tunnel page -> WebRTC -> agent
 * -> the localhost test site. Signaling goes over NOSTR; there is neither
 * a signaling server nor any carrying by hand.
 *
 * The relay is FAKE by default (in process): a CI test that depends on
 * real internet relays is fragile. To verify against real relays:
 *
 *   PEERPAGES_E2E_RELAYS="wss://nos.lol,wss://nostr.mom" npx playwright test nostr.spec.ts
 *
 * The two-device / two-network verification happens by hand in
 * docs/MANUAL-TEST.md; it cannot be automated from a single machine.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

/**
 * The environment variable for running against real relays.
 *
 * When it is set no fake relay is started, and both the agent and the
 * page use those relays. The test specific to the fake relay (dropping
 * ephemeral events) is skipped.
 */
const REAL_RELAYS = (process.env.PEERPAGES_E2E_RELAYS ?? "")
  .split(",")
  .map((r) => r.trim())
  .filter((r) => r !== "");
const USING_REAL_RELAYS = REAL_RELAYS.length > 0;

let site: TestSite;
let relay: FakeRelay | null = null;
/** The relay list the agent and the page will use. */
let relayURLs: string[] = [];

/** The `?relay=` query string (BRIEF section 4: the user can change the list). */
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
 * Starts the agent and opens the visitor page by URL.
 *
 * `?relay=` is there so a visitor can change their relay list (BRIEF
 * section 4); here it is used to point at the fake relay, so there is no
 * separate back door for the test.
 */
async function openByURL(
  context: BrowserContext,
  agent: NostrAgent,
  extraQuery = "",
): Promise<Page> {
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[page error] ${msg.text()}`);
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

test.describe("phase 2 acceptance criteria — connecting over Nostr", () => {
  test("the visitor connects with the URL alone", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
    });

    try {
      const started = Date.now();
      const tunnelPage = await openByURL(context, agent);

      // NO copy-paste: the tunnel has to open on its own.
      await expectTunnelOpen(tunnelPage);
      const elapsed = Date.now() - started;
      const relayKind = USING_REAL_RELAYS ? "gercek relay" : "sahte relay, loopback";
      console.log(`MEASURED from URL to tunnel: ${elapsed} ms (${relayKind})`);

      // The peer-id is shown shortened on screen.
      await expect(tunnelPage.locator("#nostr-peer")).toContainText(agent.npub.slice(0, 12));

      // The manual signaling panel must not show: it is not needed on
      // this path.
      await expect(tunnelPage.locator("#panel-manual")).toBeHidden();
      // The intro block must not show either: there IS a peer-id in the
      // URL, so the visitor already knows what to do (phase 7).
      await expect(tunnelPage.locator("#panel-intro")).toBeHidden();

      // Does the site really come over the tunnel?
      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site", {
        timeout: 30_000,
      });
      await expect(visitor.locator("body")).toHaveCSS("background-color", "rgb(13, 17, 23)");
      await expect(visitor.locator("body")).toHaveAttribute("data-api-ran", "yes");
      await expect(visitor.locator("#items .api-item")).toHaveCount(3);

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  test("a 5 MB file downloads over a tunnel built with Nostr", async ({ context }) => {
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
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

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
        `MEASURED 5MB download (the Nostr path): ${Math.round(result.elapsedMs ?? 0)} ms, ${mbps.toFixed(1)} MB/s`,
      );

      await visitor.close();
      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  test("it connects with a shared secret", async ({ context }) => {
    const secret = "kedi-balik-7";
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
      secret,
    });

    try {
      // The URL the agent printed already carries the secret
      // (#npub:secret).
      expect(agent.visitorURL).toContain(`:${secret}`);

      const tunnelPage = await openByURL(context, agent);
      await expectTunnelOpen(tunnelPage);

      await tunnelPage.close();
    } finally {
      agent.stop();
    }
  });

  // Someone who knows the peer-id but not the secret CANNOT CONNECT
  // (BRIEF section 4).
  test("with a secret required a visitor without one cannot connect", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs,
      appURL: APP_URL,
      secret: "right-secret",
    });

    try {
      const page = await context.newPage();
      // We STRIP the secret from the URL: only the npub is left.
      const npubOnly = agent.visitorURL.slice(agent.visitorURL.indexOf("#") + 1).split(":")[0];
      await page.goto(`${CONNECT}?${relayQuery()}#${npubOnly}`);

      // The tunnel must not open and the user has to be shown a
      // meaningful error.
      await expect(page.locator("#status-msg")).toHaveAttribute("data-tone", "error", {
        timeout: 60_000,
      });
      await expect(page.locator("#panel-connected")).toBeHidden();

      await page.close();
    } finally {
      agent.stop();
    }
  });

  test("with the agent not running a meaningful error is shown", async ({ context }) => {
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

    // After the 30 s timeout "the agent did not answer" has to show
    // up.
    await expect(page.locator("#status-msg")).toContainText("The agent did not answer", {
      timeout: 60_000,
    });
    await expect(page.locator("#btn-retry")).toBeEnabled();

    await page.close();
  });

  test("a malformed peer-id gives a meaningful error", async ({ context }) => {
    const page = await context.newPage();
    await page.goto(`${CONNECT}#npub1bozukbozukbozuk`);

    await expect(page.locator("#status-msg")).toHaveAttribute("data-tone", "error", {
      timeout: 30_000,
    });
    // The manual signaling panel must not be shown either: the user
    // should not start fiddling by hand before noticing the URL is
    // wrong.
    await expect(page.locator("#panel-manual")).toBeHidden();

    await page.close();
  });

  test("an invalid relay address gives a meaningful error", async ({ context }) => {
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

  // A relay that drops ephemeral events: the publish is accepted but
  // never forwarded. The visitor sees that as "the agent did not answer";
  // `doctor` says why (BRIEF section 8).
  test("no connection is made through a relay that drops ephemeral events", async ({ context }) => {
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

        await expect(page.locator("#status-msg")).toContainText("The agent did not answer", {
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
