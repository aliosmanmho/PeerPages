import { defineConfig } from "vite";

/**
 * App shell derlemesi.
 *
 * `base` GitHub Pages alt yolu icin build-time ayarlanir:
 *   PEERPAGES_BASE=/PeerPages/ npm run build
 * Varsayilan `/` lokal gelistirme ve `vite preview` icin uygundur.
 *
 * Service Worker BU derlemeye dahil degil; `vite.sw.config.ts` ile ayrica
 * derlenir (BRIEF: vite-plugin-pwa kullanilmaz, SW elle yazilir ve elle
 * derlenir — scope kokunde sabit `sw.js` adi gerekiyor).
 */
export default defineConfig({
  base: process.env.PEERPAGES_BASE ?? "/",
  build: {
    target: "esnext",
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
    // Iki HTML girdisi:
    //  - index.html      : scope koku. Onyukleyici; tunel acikken SW burayi
    //                      yerel sitenin kokune proxy'ler.
    //  - connect/index.html : baglanti ekrani + tunel sahibi sayfa. SW bu
    //                      yolu asla tunelden gecirmez.
    // Gerekcesi BRIEF §8.
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
