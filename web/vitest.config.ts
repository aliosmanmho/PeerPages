import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // The unit tests are pure logic: they need neither a DOM nor a
    // Service Worker environment.
    environment: "node",
    include: ["src/**/*.test.ts"],
    // The Playwright e2e suite runs separately (npm run e2e).
    exclude: ["e2e/**", "node_modules/**", "dist/**"],
  },
});
