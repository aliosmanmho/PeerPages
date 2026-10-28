import { defineConfig } from "vite";

/**
 * The app shell build.
 *
 * `base` is set at build time for the GitHub Pages subpath:
 *   PEERPAGES_BASE=/PeerPages/ npm run build
 * The default `/` suits local development and `vite preview`.
 *
 * The Service Worker is NOT part of THIS build; it is built separately
 * with `vite.sw.config.ts` (BRIEF: vite-plugin-pwa is not used, the SW is
 * written and built by hand — a fixed `sw.js` name at the scope root is
 * required).
 */
export default defineConfig({
  base: process.env.PEERPAGES_BASE ?? "/",
  build: {
    target: "esnext",
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
    // Two HTML entries:
    //  - index.html      : the scope root. The bootloader; with the tunnel
    //                      open the SW proxies this to the local site's
    //                      root.
    //  - connect/index.html : the connect screen and the tunnel-owner
    //                      page. The SW never sends this path through the
    //                      tunnel.
    // The reasoning: BRIEF section 8.
    rollupOptions: {
      input: {
        boot: "index.html",
        connect: "connect/index.html",
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
  },
  preview: {
    port: 4173,
    strictPort: true,
  },
});
