/**
 * App shell'in yayinlandigi temel yol (GitHub Pages alt yolu olabilir).
 *
 * Vite `import.meta.env.BASE_URL` olarak derleme zamani `base` degerini verir
 * ("/" ya da "/PeerPages/"). Service Worker scope'u ve app shell URL'leri bu
 * degerden turetilir — boylece SW yalnizca kendi kapsamindaki istekleri
 * yakalar (README guvenlik ilkesi 5).
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
 * Verilen URL app shell'in kendi dosyalarindan biri mi?
 *
 * Bu dosyalar tunele gonderilmez; aksi halde baglanti ekranini getirmek icin
 * tunele ihtiyac duyardik (yumurta-tavuk).
 */
export function isAppShellPath(pathname: string, base: string = BASE_PATH): boolean {
  if (!pathname.startsWith(base)) return false;
  const rest = pathname.slice(base.length);
  if (rest === "" || rest === "index.html") return true;
  return rest === "sw.js" || rest.startsWith("assets/") || rest === "favicon.ico";
}
