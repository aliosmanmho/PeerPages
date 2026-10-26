import { defineConfig } from "vite";

/**
 * App shell derlemesi.
 *
 * `base` GitHub Pages alt yolu icin build-time ayarlanir:
 *   PEERPAGES_BASE=/PeerPages/ npm run build
 * Varsayilan `/` lokal gelistirme ve `vite preview` icin uygundur.
 *
 * Service Worker BU derlemeye dahil degil; `vite.sw.config.ts` ile ayrica
 * derlenir (README: vite-plugin-pwa kullanilmaz, SW elle yazilir ve elle
 * derlenir — scope kokunde sabit `sw.js` adi gerekiyor).
 */
export default defineConfig({
  base: process.env.PEERPAGES_BASE ?? "/",
  build: {
    target: "esnext",
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
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
