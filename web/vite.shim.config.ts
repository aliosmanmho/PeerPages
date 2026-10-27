import { defineConfig } from "vite";

/**
 * WebSocket shim derlemesi — app shell ve SW'den AYRI (Faz 6).
 *
 * Neden ayri:
 *  - Dosya adi hash'siz ve sabit olmali (`dist/ws-shim.js`): Service Worker
 *    bu adi `<script src="...">` olarak enjekte ediyor ve `isAppShellPath`
 *    bu adi taniyor.
 *  - KLASIK script olmali (modul degil): ziyaretcinin sayfasina enjekte
 *    ediliyor ve sayfanin kendi script'lerinden ONCE kosmasi gerekiyor.
 *    `type="module"` ertelenmis calisirdi, yani sayfa WebSocket'i once
 *    alirdi.
 *
 * `emptyOutDir: false` — app derlemesinden sonra calisir, dist'i silmemeli.
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
      // IIFE = klasik script.
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
