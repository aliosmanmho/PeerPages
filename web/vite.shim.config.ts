import { defineConfig } from "vite";

/**
 * The WebSocket shim build — SEPARATE from the app shell and the SW
 * (phase 6).
 *
 * Why separate:
 *  - the file name has to be fixed and unhashed (`dist/ws-shim.js`): the
 *    Service Worker injects that name as `<script src="...">` and
 *    `isAppShellPath` recognises it.
 *  - it has to be a CLASSIC script (not a module): it is injected into the
 *    visitor's page and has to run BEFORE the page's own scripts. With
 *    `type="module"` it would run deferred, so the page would grab
 *    WebSocket first.
 *
 * `emptyOutDir: false` — it runs after the app build and must not wipe
 * dist.
 */
export default defineConfig({
  base: process.env.PEERPAGES_BASE ?? "/",
  build: {
    target: "es2020",
    outDir: "dist",
    emptyOutDir: false,
    sourcemap: true,
    lib: {
      entry: "src/ws-shim.ts",
      // IIFE = a classic script.
      formats: ["iife"],
      name: "PeerPagesWsShim",
      fileName: () => "ws-shim.js",
    },
    rollupOptions: {
      output: {
        entryFileNames: "ws-shim.js",
      },
    },
  },
});
