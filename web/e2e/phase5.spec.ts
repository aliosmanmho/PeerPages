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
 * The phase 5 acceptance criterion:
 *
 *   "It connects over TURN in a symmetric NAT simulation (for example a
 *    mobile hotspot <-> a corporate network)."
 *
 * We cannot imitate symmetric NAT on a single machine. Instead we build
 * the situation that produces the SAME RESULT: we force the agent with
 * `--ice-relay-only` to gather relay candidates only, so the direct path
 * is IMPOSSIBLE. The tunnel opening is proof that the traffic went over
 * TURN — stronger than a hotspot attempt, where the direct path may have
 * worked as well.
 *
 * The visitor side arrives with the NORMAL configuration: no TURN
 * credential ever reaches the page (BRIEF section 8).
 *
 * The real two-network attempt is done by hand: docs/MANUAL-TEST.md.
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

test.describe("phase 5 — connecting over TURN", () => {
  test("a relay-only agent connects over TURN", async ({ context }) => {
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
        if (msg.type() === "error") console.log(`[page error] ${msg.text()}`);
      });
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      const started = Date.now();
      await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);

      await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
      await page.waitForFunction(() => window.__peerpages?.hasTunnel() === true, null, {
        timeout: 90_000,
      });
      console.log(`MEASURED from URL to tunnel (TURN only): ${Date.now() - started} ms`);

      // The screen has to tell the truth too: the traffic goes through a
      // server.
      await page.waitForFunction(() => window.__peerpages?.iceRoute() === "relay", null, {
        timeout: 30_000,
      });
      await expect(page.locator("#metrics")).toContainText("through TURN");

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
      turn.stop();
    }
  });

  // THE NEGATIVE CONTROL: without this test the one above could pass
  // over the direct path and stay green by accident.
  test("a relay-only agent cannot connect without TURN", async ({ context }) => {
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      // An address nothing listens on: no allocation is granted, so no
      // usable candidate is gathered at all.
      turn: { url: "turn:127.0.0.1:1", user: "u", pass: "p" },
      iceRelayOnly: true,
    });

    try {
      const page = await context.newPage();
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);

      // No connection may be made; the screen status falls back to
      // "retrying" (the phase 4 supervisor) but the tunnel never
      // opens.
      await page.waitForTimeout(20_000);
      expect(await page.evaluate(() => window.__peerpages?.hasTunnel())).toBe(false);
      await expect(page.locator("#panel-connected")).toBeHidden();

      await page.close();
    } finally {
      agent.stop();
    }
  });

  // Even with TURN given, if the direct path exists ICE picks it: no
  // quota is wasted.
  test("with TURN available the direct path is still preferred", async ({ context }) => {
    const turn = await startFakeTurn();
    const agent = await startNostrAgent({
      targetURL: site.url,
      relayURLs: [relay.url],
      appURL: APP_URL,
      turn: { url: turn.url, user: turn.user, pass: turn.pass },
      // NO relay-only: the agent gathers host/srflx and relay
      // candidates.
    });

    try {
      const page = await context.newPage();
      const hash = agent.visitorURL.slice(agent.visitorURL.indexOf("#"));
      await page.goto(`${CONNECT}?relay=${encodeURIComponent(relay.url)}${hash}`);

      await expect(page.locator("#panel-connected")).toBeVisible({ timeout: 90_000 });
      await page.waitForFunction(
        () => {
          const r = window.__peerpages?.iceRoute();
          return r !== undefined && r !== "unknown";
        },
        null,
        { timeout: 60_000 },
      );

      const route = await page.evaluate(() => window.__peerpages?.iceRoute());
      console.log(`MEASURED TURN was given but the chosen route: ${route}`);
      expect(route).not.toBe("relay");

      await page.close();
    } finally {
      agent.stop();
      turn.stop();
    }
  });
});

test.describe("phase 5 — the doctor TURN report", () => {
  test("doctor verifies the allocation", async () => {
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
      console.log(`MEASURED doctor TURN allocation: ${report.turn.servers[0].ms} ms`);

      // The wrong password: no allocation is granted and the exit code
      // has to be non-zero — returning 0 quietly would let the user think
      // TURN works.
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
        "wrong-password",
      ]);
      expect(bad.code).not.toBe(0);
      const badReport = JSON.parse(bad.stdout);
      expect(badReport.turn.servers[0].ok).toBe(false);
    } finally {
      turn.stop();
    }
  });
});
