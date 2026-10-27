import { defineConfig } from "vite";

/**
 * Faz 6 e2e fixture'i: HMR'in tunel uzerinden calistigini dogrulamak icin
 * kucuk bir Vite dev projesi.
 *
 * `base` app shell'in scope koku ile AYNI olmali ve ajan hedefi de o oneki
 * tasimali (`--target http://127.0.0.1:PORT/PeerPages/`). Gerekcesi:
 * Service Worker scope onekini kirparak ajana yolluyor, Vite ise onekli
 * yollar bekliyor; iki tarafi hizalamak, sayfanin urettigi KOK-MUTLAK
 * yollarin scope icinde kalmasini da sagliyor (README §8'deki yapisal
 * sinir).
 *
 * HMR icin hicbir ozel ayar YOK ve bu bilincli: kabul kriteri
 * "Vite HMR tunel uzerinden calisiyor" ise, kullanicinin HMR
 * yapilandirmasina dokunmadan calismasi gerekir.
 */
export default defineConfig({
  base: process.env.PEERPAGES_FIXTURE_BASE ?? "/PeerPages/",
  server: {
    host: "127.0.0.1",
    strictPort: false,
  },
});
