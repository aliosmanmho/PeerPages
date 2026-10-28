import { defineConfig, devices } from "@playwright/test";

/**
 * The e2e setup that verifies the acceptance criteria in a real browser.
 *
 * The setup:
 *  - `vite preview` serves the built app shell under the `/PeerPages/`
 *    subpath (a GitHub Pages-like subpath). Because localhost counts as a
 *    secure context, the Service Worker works.
 *  - The tests start the agent and the test site as child processes
 *    themselves and carry the offer and answer over stdout/stdin
 *    (fixtures/agent.ts).
 *
 * The manual path needs NO signaling server and no Nostr (BRIEF phase 1);
 * the Nostr path uses an in-process fake relay.
 */

const PORT = 4173;
const BASE = "/PeerPages/";
// `localhost` is used because vite preview listens on IPv6 (::1) and
// 127.0.0.1 cannot be reached. localhost counts as a secure context too,
// so the Service Worker works.
const ORIGIN = `http://localhost:${PORT}`;

export default defineConfig({
  testDir: "e2e",
  // It builds the web app with the right subpath, generates the test data
  // and builds the agent.
  globalSetup: "./e2e/global-setup.ts",
  // One tunnel, one origin: running in parallel would collide.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // `demo.spec.ts`, which produces the README images, is SKIPPED in a
  // normal run: it is a production script, not a test. Run it by hand:
  //   Git Bash  : PEERPAGES_DEMO=1 npx playwright test demo
  //   PowerShell: $env:PEERPAGES_DEMO=1; npx playwright test demo
  grepInvert: process.env.PEERPAGES_DEMO ? undefined : /@demo/,
  retries: process.env.CI ? 1 : 0,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],

  use: {
    baseURL: `${ORIGIN}${BASE}`,
    trace: process.env.CI ? "retain-on-failure" : "off",
    // Generous limits for the 5 MB download and the ICE setup.
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  webServer: {
    // The build happens HERE rather than in globalSetup: Playwright
    // starts webServer BEFORE globalSetup, and with no dist yet the
    // preview exits. `base` is passed through an environment variable
    // rather than the CLI (scripts/build.mjs), so the build and the
    // preview use the same value.
    command: `node scripts/build.mjs ${BASE} && npx vite preview --port ${PORT} --strictPort`,
    env: { PEERPAGES_BASE: BASE },
    url: `${ORIGIN}${BASE}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
