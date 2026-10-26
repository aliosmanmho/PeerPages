import { defineConfig, devices } from "@playwright/test";

/**
 * Faz 1 kabul kriterlerini gercek bir tarayicida dogrulayan e2e kurulumu.
 *
 * Kurulum:
 *  - `vite preview` derlenmis app shell'i `/PeerPages/` alt yolunda servis
 *    eder (GitHub Pages benzeri bir alt yol). localhost guvenli baglam
 *    sayildigi icin Service Worker calisir.
 *  - Test kendisi ajani ve test sitesini child process olarak baslatir,
 *    offer/answer'i stdout/stdin uzerinden tasir (fixtures/agent.ts).
 *
 * Signaling sunucusu ve Nostr YOK (README Faz 1).
 */

const PORT = 4173;
const BASE = "/PeerPages/";
// `localhost` kullaniliyor cunku vite preview IPv6 (::1) uzerinde dinliyor;
// 127.0.0.1'e baglanilamiyor. localhost da guvenli baglam sayildigi icin
// Service Worker calisir.
const ORIGIN = `http://localhost:${PORT}`;

export default defineConfig({
  testDir: "e2e",
  // Web'i dogru alt yolla derler, test verisini uretir, ajani derler.
  globalSetup: "./e2e/global-setup.ts",
  // Tek tunel, tek origin: paralel kosum birbirine girer.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],

  use: {
    baseURL: `${ORIGIN}${BASE}`,
    trace: process.env.CI ? "retain-on-failure" : "off",
    // 5 MB indirme ve ICE kurulumu icin comert sinirlar.
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
    // Derleme BURADA yapiliyor, globalSetup'ta degil: Playwright webServer'i
    // globalSetup'tan ONCE baslatiyor ve dist henuz yoksa preview cikiyor.
    // `base` CLI yerine ortam degiskeniyle veriliyor (scripts/build.mjs);
    // boylece derleme ile preview ayni degeri kullaniyor.
    command: `node scripts/build.mjs ${BASE} && npx vite preview --port ${PORT} --strictPort`,
    env: { PEERPAGES_BASE: BASE },
    url: `${ORIGIN}${BASE}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
