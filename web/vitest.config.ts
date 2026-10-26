import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Birim testler saf mantik: DOM ya da Service Worker ortami gerektirmez.
    environment: "node",
    include: ["src/**/*.test.ts"],
    // Playwright e2e ayri calisir (npm run e2e).
    exclude: ["e2e/**", "node_modules/**", "dist/**"],
  },
});
