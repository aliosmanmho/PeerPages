import { WS_SHIM_FILE } from "./bridge";

/**
 * App shell'in yayinlandigi temel yol (GitHub Pages alt yolu olabilir).
 *
 * Vite `import.meta.env.BASE_URL` olarak derleme zamani `base` degerini verir
 * ("/" ya da "/PeerPages/"). Service Worker scope'u ve app shell URL'leri bu
 * degerden turetilir — boylece SW yalnizca kendi kapsamindaki istekleri
 * yakalar (BRIEF guvenlik ilkesi 5).
 */
export const BASE_PATH: string = normalizeBase(import.meta.env.BASE_URL);

function normalizeBase(raw: string | undefined): string {
  if (!raw || raw === "./") return "/";
  let base = raw;
  if (!base.startsWith("/")) base = `/${base}`;
  if (!base.endsWith("/")) base = `${base}/`;
  return base;
}

/** Scope kokune gore mutlak yol uretir: `inBase("sw.js")` → "/PeerPages/sw.js". */
export function inBase(path: string): string {
  return BASE_PATH + path.replace(/^\/+/, "");
}

/**
 * Baglanti ekrani ve tunel sahibi sayfanin yolu.
 *
 * Scope kokunde DEGIL: scope koku yerel sitenin koku. Boylece
 *  - ziyaretci sitenin kokunu dogal URL'de gorur,
 *  - tunel sahibi sekmenin URL'si hicbir zaman tunelden gecmez, yani o sekme
 *    yenilendiginde tunel kendini kapatmaz.
 * Karari ve gerekcesi: BRIEF §8.
 */
export const CONNECT_PATH: string = `${BASE_PATH}connect/`;

/**
 * Verilen URL app shell'in kendi dosyalarindan biri mi?
 *
 * Bu dosyalar tunele GONDERILMEZ; aksi halde baglanti ekranini getirmek icin
 * tunele ihtiyac duyardik (yumurta-tavuk).
 *
 * Scope kokunun KENDISI app shell degil: orasi yerel sitenin koku ve tunel
 * acikken tunelden gecer. Tunel yoksa SW navigasyonu baglanti ekranina
 * yonlendirir, alt kaynaklara 503 doner (src/sw.ts).
 */
export function isAppShellPath(pathname: string, base: string = BASE_PATH): boolean {
  if (!pathname.startsWith(base)) return false;
  const rest = pathname.slice(base.length);
  return (
    rest === "connect" ||
    rest === "connect/" ||
    rest === "connect/index.html" ||
    rest === "sw.js" ||
    rest === "favicon.ico" ||
    // WebSocket shim (Faz 6): app shell'in kendi dosyasi. Tunelden
    // GECMEMELI, aksi halde ziyaretcinin sitesinde boyle bir dosya aranir ve
    // 404 doner.
    rest === WS_SHIM_FILE ||
    rest.startsWith("assets/")
  );
}
