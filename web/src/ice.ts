/**
 * Ziyaretci tarafindaki ICE yapilandirmasi.
 *
 * Faz 5'e kadar bu sayfa HIC ICE sunucusu vermiyordu
 * (`new RTCPeerConnection(undefined)`), yani yalnizca **host adayi**
 * topluyordu: aglar arasi baglanti, ajanin srflx adayina gelen paketten
 * ziyaretciyi peer-reflexive ogrenmesine bel baglamis durumdaydi. Varsayilan
 * STUN listesiyle ziyaretci de kendi srflx adayini topluyor ve dogrudan yol
 * daha cok NAT'ta kuruluyor.
 *
 * **TURN kimlik bilgisi buraya HIC gelmiyor.** TURN yalnizca ajan tarafinda
 * kullaniliyor: ziyaretci offer'i ajandan tek kelime duymadan once uretiyor,
 * yani kimlik bilgisini signaling ile gondermek cok gec kalirdi. Tek tarafli
 * TURN yeterli — ajan relay adayi toplar, ziyaretci o genel adrese paket
 * yollar. Kota ve sir sahibinde kalir (README §6 ilke 4: ziyaretci
 * guvenilmez). Ayrinti: README §8.
 */

export class IceConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "IceConfigError";
  }
}

/**
 * Varsayilan STUN listesi.
 *
 * Go tarafindaki `ice.DefaultSTUN` ile AYNI olmali.
 */
export const DEFAULT_STUN = [
  "stun:stun.l.google.com:19302",
  "stun:stun1.l.google.com:19302",
] as const;

/** Tek bir STUN adresini dogrular ve normalize eder. */
export function normalizeStunURL(raw: string): string {
  const s = raw.trim();
  if (s === "") {
    throw new IceConfigError("STUN adresi bos");
  }
  // STUN URI'leri opaque: "stun:host:port" — `//` yok, bu yuzden URL()
  // host'u ayristirmiyor ve semayi elle kontrol ediyoruz.
  const sep = s.indexOf(":");
  if (sep < 0) {
    throw new IceConfigError(`STUN adresi stun: ya da stuns: ile baslamali: ${s}`);
  }
  const scheme = s.slice(0, sep);
  if (scheme !== "stun" && scheme !== "stuns") {
    throw new IceConfigError(`STUN adresi stun: ya da stuns: ile baslamali: ${s}`);
  }

  let host = s.slice(sep + 1);
  if (host === "") {
    throw new IceConfigError(`STUN adresinde host yok: ${s}`);
  }
  if (host.includes("?") || host.includes("/")) {
    throw new IceConfigError(`STUN adresinde yol ya da query olamaz: ${s}`);
  }
  // Port verilmemisse varsayilani ekle (IPv6 koseli parantez icinde gelir).
  const lastColon = host.lastIndexOf(":");
  const closing = host.lastIndexOf("]");
  if (lastColon <= closing) {
    host = `${host}:${scheme === "stuns" ? 5349 : 3478}`;
  }
  return `${scheme}:${host}`;
}

/**
 * URL sorgu dizesinden STUN listesini okur.
 *
 * `stun` parametresi tekrarlanabilir; virgulle ayrilmis liste de kabul
 * edilir. Parametre yoksa `null` doner (varsayilan liste kullanilir).
 * Bos deger (`?stun=`) ICE sunucusunu tamamen kapatir: testlerde yalnizca
 * host adayi toplamanin tek yolu bu.
 *
 * @throws {IceConfigError} verilen adreslerden biri gecersizse — sessizce
 * varsayilana donmek, kullanicinin verdigi listenin yok sayildigini
 * gizlerdi (`relaysFromSearch` ile ayni karar).
 */
export function stunFromSearch(search: string): string[] | null {
  const params = new URLSearchParams(search);
  const raw = params.getAll("stun");
  if (raw.length === 0) return null;

  const items = raw.flatMap((v) => v.split(","));
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    if (item.trim() === "") continue;
    const url = normalizeStunURL(item);
    if (seen.has(url)) continue;
    seen.add(url);
    out.push(url);
  }
  // `?stun=` (bos) bilincli bir secim: ICE sunucusu yok.
  return out;
}

/** Verilen listeden (ya da varsayilandan) RTCIceServer dizisi uretir. */
export function iceServers(stun: readonly string[] | null): RTCIceServer[] {
  const list = stun ?? DEFAULT_STUN;
  if (list.length === 0) return [];
  return [{ urls: [...list] }];
}

/** Baglantinin hangi yoldan kuruldugu. */
export type IceRoute = "host" | "srflx" | "prflx" | "relay" | "bilinmiyor";

/** Ekranda gosterilecek metin. */
export function routeLabel(route: IceRoute): string {
  switch (route) {
    case "host":
      return "dogrudan (yerel ag)";
    case "srflx":
      return "dogrudan (NAT uzerinden)";
    case "prflx":
      return "dogrudan (peer-reflexive)";
    case "relay":
      return "TURN uzerinden (relay)";
    default:
      return "olculuyor…";
  }
}

/**
 * Secilen aday ciftinden baglanti yolunu cikarir.
 *
 * Neden gerekli: "trafik bir sunucudan geciyor mu" sorusunun bugune kadar
 * cevabi yoktu. TURN kullanildiginda ucten uca sifreleme surer (TURN
 * sunucusu icerigi goremez, yalnizca baytlari tasir) ama YOL degisir ve
 * ziyaretcinin bunu gormesi dogru olan.
 *
 * Iki tarafin aday tipi farkli olabilir; "relay" taraflardan birinde varsa
 * trafik relay'den geciyor demektir.
 */
export function routeFromStats(report: RTCStatsReport): IceRoute {
  let localId: string | undefined;
  let remoteId: string | undefined;
  let found = false;

  report.forEach((entry) => {
    const stat = entry as RTCStats & {
      type: string;
      selected?: boolean;
      state?: string;
      nominated?: boolean;
      localCandidateId?: string;
      remoteCandidateId?: string;
    };
    if (stat.type !== "candidate-pair") return;
    // Chromium `selected` yerine state=succeeded + nominated kullaniyor;
    // Firefox `selected` veriyor. Ikisini de kabul ediyoruz.
    const chosen =
      stat.selected === true || (stat.state === "succeeded" && stat.nominated === true);
    if (!chosen) return;
    found = true;
    localId = stat.localCandidateId;
    remoteId = stat.remoteCandidateId;
  });

  if (!found) return "bilinmiyor";

  const typeOf = (id?: string): IceRoute | null => {
    if (!id) return null;
    const cand = report.get(id) as (RTCStats & { candidateType?: string }) | undefined;
    switch (cand?.candidateType) {
      case "host":
      case "srflx":
      case "prflx":
      case "relay":
        return cand.candidateType;
      default:
        return null;
    }
  };

  const local = typeOf(localId);
  const remote = typeOf(remoteId);

  // Taraflardan biri relay ise trafik relay'den geciyor.
  if (local === "relay" || remote === "relay") return "relay";
  return local ?? remote ?? "bilinmiyor";
}
