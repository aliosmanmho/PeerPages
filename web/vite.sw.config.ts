import { defineConfig } from "vite";

/**
 * Service Worker derlemesi — app shell'den AYRI.
 *
 * Neden ayri:
 *  - SW dosyasinin adi hash'siz ve sabit olmali (`dist/sw.js`), cunku scope
 *    kokunde `register(base + "sw.js", { scope: base })` ile kaydediliyor.
 *  - SW'nin ne bundle'landigi uzerinde tam kontrol lazim (README karari).
 *
 * `emptyOutDir: false` — app derlemesinden sonra calisir, dist'i silmemeli.
 * Bu yuzden `npm run build` sirasi: build:app, sonra build:sw.
 */
export default defineConfig({
  // `base` burada gereksiz: SW cikti dosyasi hicbir asset URL'si barindirmiyor.
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
    // SW tek dosya olmali: ayri chunk'lar scope kokunde ek istek demek.
    rollupOptions: {
      output: {
        entryFileNames: "sw.js",
        codeSplitting: false,
      },
    },
  },
});
