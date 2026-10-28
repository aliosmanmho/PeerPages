import { expect, test } from "@playwright/test";

import {
  type Agent,
  bigFileSHA256,
  startAgent,
  startTestSite,
  type TestSite,
} from "./fixtures/agent";

/**
 * The phase 1 acceptance criteria (BRIEF phase 1):
 *
 *   "In the visitor's browser the test site (HTML, CSS, JS, an image,
 *    JSON over fetch) renders in full on a github.io-like origin; a 5 MB
 *    file download works; on a refresh, with no tunnel, the SW falls back
 *    to the connect screen."
 *
 * The chain: browser -> Service Worker -> tunnel page -> WebRTC -> agent
 * -> the localhost test site. There is NO signaling server in between;
 * this test carries the offer and answer over stdin/stdout (the page
 * creates the offer, the agent answers).
 */

const BASE = "/PeerPages/";
// The connect screen is NOT at the scope root: that root belongs to the
// local site (the decision and its reasoning: BRIEF section 8).
const CONNECT = `${BASE}connect/`;

let site: TestSite;
let agent: Agent;

test.beforeAll(async () => {
  site = await startTestSite();
});

test.afterAll(() => {
  site?.stop();
});

// The agent is restarted PER TEST: `peerpages serve` runs a single
// tunnel session and exits when it closes, so its offer is one-shot.
test.beforeEach(async () => {
  agent = await startAgent(site.url);
});

test.afterEach(() => {
  agent?.stop();
});

/**
 * Opens the connect screen, installs the Service Worker and opens the
 * tunnel with manual signaling. The page it returns OWNS the tunnel: if it
 * closes, the tunnel closes.
 *
 * THE DIRECTION: the page creates the offer -> it goes to the agent over
 * stdin -> the agent prints the answer -> it is pasted into the page.
 */
async function openTunnelPage(context: import("@playwright/test").BrowserContext) {
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`[page error] ${msg.text()}`);
  });

  await page.goto(CONNECT);

  // Wait for the Service Worker to install. We look at the STEP STATE
  // rather than the status TEXT: the text moves on to the next step right
  // away (for example "no peer-id — the manual signaling path"), so it is
  // not a stable signal.
  await expect(page.locator('.status__step[data-step="sw"]')).toHaveAttribute(
    "data-state",
    "done",
    { timeout: 30_000 },
  );
  // With no peer-id in the URL the manual signaling panel has to be
  // open.
  await expect(page.locator("#panel-manual")).toBeVisible();
  // ...and the intro block ABOVE IT: someone arriving at this screen
  // from the README should first see what it is (phase 7).
  await expect(page.locator("#panel-intro")).toBeVisible();

  // The PAGE creates the offer (the phase 2 role swap).
  await page.locator("#btn-make-offer").click();
  await expect(page.locator("#offer-output")).not.toHaveValue("", { timeout: 40_000 });
  const offer = await page.locator("#offer-output").inputValue();
  expect(offer.length).toBeGreaterThan(20);

  // The agent answers; we paste it into the page.
  const answer = await agent.exchange(offer);
  await page.locator("#answer-input").fill(answer);
  await page.locator("#btn-accept-answer").click();

  await agent.waitTunnelOpen();
  await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 30_000 });
  await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
    timeout: 30_000,
  });
  // Once the tunnel opens the intro and the manual flow go away: only
  // the status is left on screen.
  await expect(page.locator("#panel-intro")).toBeHidden();
  await expect(page.locator("#panel-manual")).toBeHidden();

  return page;
}

test.describe("phase 1 acceptance criteria", () => {
  test("the test site works in full over the tunnel", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);

    // The tunnel page has to stay open; we open the site in a SEPARATE
    // tab.
    const visitor = await context.newPage();
    const errors: string[] = [];
    visitor.on("console", (msg) => {
      if (msg.type() === "error") errors.push(msg.text());
    });

    await visitor.goto(BASE);

    await test.step("HTML render oluyor", async () => {
      await expect(visitor.locator("#title")).toHaveText("PeerPages test site", {
        timeout: 30_000,
      });
    });

    await test.step("CSS uygulandi", async () => {
      // This colour cannot appear unless style.css came over the
      // tunnel.
      await expect(visitor.locator("body")).toHaveCSS("background-color", "rgb(13, 17, 23)");
      await expect(visitor.locator("#title")).toHaveCSS("color", "rgb(88, 166, 255)");
    });

    await test.step("JS calisti", async () => {
      await expect(visitor.locator("body")).toHaveAttribute("data-js-ran", "yes");
    });

    await test.step("the image loaded", async () => {
      const natural = await visitor
        .locator("#logo")
        .evaluate((img) => (img as HTMLImageElement).naturalWidth);
      expect(natural).toBe(64);
    });

    await test.step("fetch ile JSON geldi", async () => {
      await expect(visitor.locator("body")).toHaveAttribute("data-api-ran", "yes");
      await expect(visitor.locator("#items .api-item")).toHaveCount(3);
      await expect(visitor.locator("#items .api-item").first()).toHaveText("alpha = 1");
    });

    expect(errors, `there should be no console error on the page: ${errors.join(" | ")}`).toEqual(
      [],
    );

    await visitor.close();
    await tunnelPage.close();
  });

  test("a 5 MB file download works", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

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

    expect(result.error, `the download failed: ${result.error}`).toBeUndefined();
    expect(result.bytes).toBe(5 * 1024 * 1024);
    expect(result.sha).toBe(expectedSha);

    const mbps = 5 / ((result.elapsedMs ?? 1) / 1000);
    console.log(
      `MEASURED 5MB download (browser, SW+WebRTC): ${Math.round(result.elapsedMs ?? 0)} ms, ` +
        `${mbps.toFixed(1)} MB/s`,
    );

    await visitor.close();
    await tunnelPage.close();
  });

  test("with no tunnel a navigation falls back to the connect screen", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);

    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

    // Close the tunnel-owner page: the WebRTC connection was held in
    // it.
    await tunnelPage.close();

    // The refresh: with no tunnel to find, the SW has to redirect to the
    // connect screen.
    await visitor.reload();
    await expect(visitor.locator(".shell__title")).toHaveText("PeerPages", { timeout: 30_000 });
    await expect(visitor.locator("#panel-manual")).toBeVisible();
    expect(new URL(visitor.url()).pathname).toBe(CONNECT);

    await visitor.close();
  });

  test("with no tunnel a subresource answers 503", async ({ context }) => {
    // Build the tunnel and load the SW first, then close the tunnel.
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");
    await tunnelPage.close();

    const status = await visitor.evaluate(async () => {
      const res = await fetch("api/items", { cache: "no-store" });
      return res.status;
    });
    expect(status).toBe(503);

    await visitor.close();
  });

  test("latency and throughput are measured", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);

    // The ping/pong RTT on the ctrl channel.
    const rtts: number[] = [];
    for (let i = 0; i < 10; i++) {
      const rtt = await tunnelPage.evaluate(() => window.__peerpages?.ping());
      if (typeof rtt === "number") rtts.push(rtt);
    }
    const medianRtt = median(rtts);
    expect(medianRtt).toBeGreaterThan(0);

    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

    // Small GET latency (SW + tunnel + local server).
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
      `MEASURED ctrl ping RTT (browser): median ${medianRtt.toFixed(2)} ms (n=${rtts.length})`,
    );
    console.log(
      `MEASURED small GET (SW+tunnel): median ${median(small).toFixed(2)} ms, ` +
        `p95 ${p95.toFixed(2)} ms (n=${small.length})`,
    );

    await visitor.close();
    await tunnelPage.close();
  });

  test("on a redirect Location is rewritten to a relative path", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

    // /redirect returns an absolute Location; the agent rewrites it to a
    // relative path, which keeps the browser on our origin.
    const originBefore = new URL(visitor.url()).origin;
    await visitor.goto(`${BASE}redirect`);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

    // The redirect stayed on our origin: the agent rewrote the absolute
    // Location to a relative path and the browser never tried to reach
    // the local server.
    const after = new URL(visitor.url());
    expect(after.origin).toBe(originBefore);
    expect(after.pathname.startsWith(BASE)).toBe(true);

    await visitor.close();
    await tunnelPage.close();
  });

  test("SSE arrives as a stream", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

    const text = await visitor.evaluate(async () => {
      const res = await fetch("sse", { cache: "no-store" });
      return res.text();
    });
    expect(text).toContain("data: event-0");
    expect(text).toContain("data: event-2");

    await visitor.close();
    await tunnelPage.close();
  });

  test("the agent makes no request outside the allowlist", async ({ context }) => {
    const tunnelPage = await openTunnelPage(context);
    const visitor = await context.newPage();
    await visitor.goto(BASE);
    await expect(visitor.locator("#title")).toHaveText("PeerPages test site");

    // 1) A plain `..`: the browser normalizes the URL and the request
    //    leaves the scope. The SW does not catch it and the static server
    //    answers 404, so it never enters the tunnel at all.
    const plain = await visitor.evaluate(async () => {
      const res = await fetch("../../etc/passwd", { cache: "no-store" });
      return { status: res.status, url: res.url };
    });
    expect(plain.status).toBe(404);
    expect(new URL(plain.url).pathname.startsWith(BASE)).toBe(false);

    // 2) A percent-encoded slash (`%2f`): the URL parser does NOT decode
    //    it, so the path stays INSIDE the scope and the request really
    //    does enter the tunnel. The agent decodes it, sees the `..` and
    //    refuses (BRIEF principle 1).
    //    Note: `%2e%2e` is normalized to `..` on the browser side and
    //    falls outside the scope, so it never reaches the agent. That the
    //    agent refuses encoded traversal is verified on the Go side
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
