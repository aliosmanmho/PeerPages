/**
 * Ziyaretcinin relay listesini URL'den degistirmesi.
 *
 * BRIEF bolum 4: "Varsayilan relay listesi (en az 3, **kullanici
 * degistirebilir**)". Bu dosya o degistirmeyi uyguluyor:
 *
 *     .../connect/?relay=wss://nos.lol&relay=wss://nostr.mom#npub1…
 *
 * Ayni zamanda Nostr e2e testinin yerel sahte relay'e baglanmasini sagliyor,
 * yani test icin ayri bir arka kapi yok.
 */

export class RelayListError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RelayListError";
  }
}

/** Tek bir relay adresini dogrular ve normalize eder (Go tarafiyla ayni kural). */
export function normalizeRelayURL(raw: string): string {
  const s = raw.trim();
  if (s === "") {
    throw new RelayListError("relay adresi bos");
  }

  let u: URL;
  try {
    u = new URL(s);
  } catch {
    throw new RelayListError(`relay adresi okunamadi: ${s}`);
  }
  if (u.protocol !== "ws:" && u.protocol !== "wss:") {
    throw new RelayListError(`relay adresi ws:// ya da wss:// olmali: ${s}`);
  }
  if (u.hostname === "") {
    throw new RelayListError(`relay adresinde host yok: ${s}`);
  }

  u.hash = "";
  u.search = "";
  // Sondaki bolu atilir ki ayni relay iki kez listelenmesin.
  const text = u.toString();
  return text.endsWith("/") ? text.slice(0, -1) : text;
}

/**
 * URL sorgu dizesinden relay listesini okur.
 *
 * `relay` parametresi tekrarlanabilir; virgulle ayrilmis liste de kabul
 * edilir. Parametre yoksa `null` doner (varsayilan liste kullanilir).
 *
 * @throws {RelayListError} verilen adreslerden biri gecersizse — sessizce
 * varsayilana donmek, kullanicinin verdigi listenin yok sayildigini gizlerdi.
 */
export function relaysFromSearch(search: string): string[] | null {
  const params = new URLSearchParams(search);
  const raw = params.getAll("relay").flatMap((v) => v.split(","));
  if (raw.length === 0) return null;

  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of raw) {
    if (item.trim() === "") continue;
    const url = normalizeRelayURL(item);
    if (seen.has(url)) continue;
    seen.add(url);
    out.push(url);
  }
  if (out.length === 0) {
    throw new RelayListError("relay parametresi verildi ama bos");
  }
  return out;
}
