import { createServer, type Server } from "node:http";
import { type BrowserContext, expect, type Page, test } from "@playwright/test";

import { startTestSite, type TestSite } from "./fixtures/agent";
import { type FakeRelay, type NostrAgent, startFakeRelay, startNostrAgent } from "./fixtures/nostr";

/**
 * The automated counterpart of the phase 3 acceptance criterion:
 *
 *   "A single agent shares two projects; the screen carries meaningful
 *    status information."
 *
 * We use a small node server for the second target: if two copies served
 * the same test site, the claim "every visitor sees THEIR OWN site" could
 * not be verified — the responses have to be distinguishable.
 *
 * The two-device / two-network verification (and that the QR really is
 * readable) happens by hand in docs/MANUAL-TEST.md; it cannot be
 * automated from a single machine.
 */

const BASE = "/PeerPages/";
const CONNECT = `${BASE}connect/`;
const APP_URL = "http://localhost:4173/PeerPages/";

const ADMIN_MARKER = "ADMIN PANEL";

let site: TestSite;
let relay: FakeRelay;
let admin: { url: string; stop: () => void };

/** The second target: a tiny server that announces itself. */
async function startAdminSite(): Promise<{ url: string; stop: () => void }> {
  const server: Server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(`<!doctype html><html><body><h1 id="title">${ADMIN_MARKER}</h1></body></html>`);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address();
  if (addr === null || typeof addr === "string") throw new Error("the admin site has no address");
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

/** Opens the URL the agent printed (with its target). */
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
    if (msg.type() === "error") console.log(`[page error] ${msg.text()}`);
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

/** Starts the agent with two targets. */
function startTwoTargetAgent(): Promise<NostrAgent> {
  return startNostrAgent({
    targetURL: site.url,
    extraTargets: { admin: admin.url },
    relayURLs: [relay.url],
    appURL: APP_URL,
  });
}

test.describe("phase 3 acceptance criteria — several targets", () => {
  test("a single agent shares two projects", async ({ context }) => {
    const agent = await startTwoTargetAgent();

    try {
      // The agent has to print a separate URL for every target.
      expect(Object.keys(agent.urls).sort()).toEqual(["admin", "default"]);
      expect(agent.urls.admin).toContain(`#${agent.npub}/admin`);
      expect(agent.urls.default).toContain(`#${agent.npub}`);
      expect(agent.urls.default).not.toContain("/admin");

      // 1) The default target: the test site.
      const defaultTab = await openByURL(context, agent, "default");
      await expectTunnelOpen(defaultTab);

      const visitor = await context.newPage();
      await visitor.goto(BASE);
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site", {
        timeout: 30_000,
      });
      await visitor.close();

      // Close the tunnel-owner tab, so the second target starts
      // clean.
      await defaultTab.close();

      // 2) The admin target: the SAME agent, a DIFFERENT project.
      const adminTab = await openByURL(context, agent, "admin");
      await expectTunnelOpen(adminTab);
      await expect(adminTab.locator("#nostr-peer")).toContainText("/ admin");

      const adminVisitor = await context.newPage();
      await adminVisitor.goto(BASE);
      await expect(adminVisitor.locator("#title")).toHaveText(ADMIN_MARKER, { timeout: 30_000 });
      await adminVisitor.close();
      await adminTab.close();
    } finally {
      agent.stop();
    }
  });

  // No silent fallback to the default: the visitor may not notice they
  // are browsing the wrong project.
  test("an unknown target gives a meaningful error", async ({ context }) => {
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

  test("an invalid target name is caught in the URL", async ({ context }) => {
    // A valid npub is needed: the peer-id check comes BEFORE the target
    // check, so otherwise the test would measure the checksum error
    // rather than the target. We start the agent only to get an npub and
    // shut it down right away — this check is entirely on the CLIENT
    // side and no message should reach the agent.
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });
    const npub = agent.npub;
    agent.stop();

    const page = await context.newPage();
    await page.goto(`${CONNECT}?${relayQuery()}#${npub}/-gecersiz`);
    await expect(page.locator("#status-msg")).toContainText("invalid target name", {
      timeout: 30_000,
    });
    await expect(page.locator("#panel-connected")).toBeHidden();
    await page.close();
  });
});

test.describe("phase 3 — the connect screen", () => {
  test("the status, the copy button and the QR are shown", async ({ context }) => {
    const agent = await startTwoTargetAgent();

    try {
      const page = await openByURL(context, agent, "admin");
      await expectTunnelOpen(page);

      // The measurements: latency is measured and the target list comes
      // from hello.
      const metrics = page.locator("#metrics");
      await expect(metrics).toContainText("Latency");
      await expect(metrics).toContainText("ms", { timeout: 30_000 });
      await expect(metrics).toContainText("admin, default");
      await expect(metrics).toContainText("Active target");

      // The sharing block: the address and two buttons.
      await expect(page.locator("#share-url")).toContainText(agent.npub.slice(0, 12));
      await expect(page.locator("#btn-copy-url")).toBeVisible();

      // The QR panel starts closed, opens with the button, and the
      // canvas really is drawn.
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
      // Catch an empty or all-black canvas too: a QR's dark module ratio
      // sits in a plausible range.
      expect(drawn?.darkRatio ?? 0).toBeGreaterThan(0.2);
      expect(drawn?.darkRatio ?? 1).toBeLessThan(0.6);
      console.log(
        `MEASURED QR canvas: ${drawn?.size} px, dark ratio ${(drawn?.darkRatio ?? 0).toFixed(3)}`,
      );

      await page.close();
    } finally {
      agent.stop();
    }
  });

  test("the request counters go up", async ({ context }) => {
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
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site", {
        timeout: 30_000,
      });
      await visitor.close();

      const stats = await page.evaluate(() => window.__peerpages?.stats());
      expect(stats?.requests ?? 0).toBeGreaterThan(0);
      expect(stats?.tunnel?.bytesIn ?? 0).toBeGreaterThan(0);

      // It has to show up on screen too.
      await expect(page.locator("#metrics")).toContainText("Requests");
      await page.close();
    } finally {
      agent.stop();
    }
  });
});

test.describe("phase 3 — resources are released once the visitor is gone", () => {
  // The agent-side idle check: when the tab closes (or the notice never
  // arrives) the session closes by its own clock. We pass short durations
  // here, or the test would have to wait 30 seconds.
  test("when the owner tab closes the session closes", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
    });

    try {
      const page = await openByURL(context, agent, "default");
      await expectTunnelOpen(page);

      // The page sends a `bye` as it closes; the agent has to release
      // the session.
      await page.close();

      await expect
        .poll(
          () =>
            agent.output().some((l) => l.includes("tunnel closed") || l.includes("session idle")),
          { timeout: 60_000, message: "ajan oturumu birakmadi" },
        )
        .toBe(true);
    } finally {
      agent.stop();
    }
  });
});
