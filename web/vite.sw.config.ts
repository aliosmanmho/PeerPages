import { defineConfig } from "vite";

/**
 * The Service Worker build — SEPARATE from the app shell.
 *
 * Why separate:
 *  - the SW file name has to be fixed and unhashed (`dist/sw.js`), because
 *    it is registered at the scope root with
 *    `register(base + "sw.js", { scope: base })`.
 *  - full control over what gets bundled into the SW is needed (a BRIEF
 *    decision).
 *
 * `emptyOutDir: false` — it runs after the app build and must not wipe
 * dist. That is why the `npm run build` order is build:app, then
 * build:sw.
 */
export default defineConfig({
  // The SAME base as the app build: the SW produces no asset URL today,
  // but the two builds carrying different base values caused a bug once.
  // (The scope is still read at runtime from registration.scope.)
  base: process.env.PEERPAGES_BASE ?? "/",
  build: {
    target: "esnext",
    outDir: "dist",
    emptyOutDir: false,
    sourcemap: true,
    lib: {
      entry: "src/sw.ts",
      formats: ["es"],
      fileName: () => "sw.js",
    },
    // The SW has to be a single file: separate chunks mean extra
    // requests at the scope root.
    rollupOptions: {
      output: {
        entryFileNames: "sw.js",
        codeSplitting: false,
      },
    },
  },
});
