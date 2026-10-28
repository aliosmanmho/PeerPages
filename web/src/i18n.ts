/**
 * Tiny translation layer for the connect screen. No dependencies.
 *
 * English is the default. Turkish is offered because the project was built
 * in Turkish and its first users read it; any further language is a new
 * entry in `dict` plus the same keys.
 *
 * Why no library: the whole surface is ~110 strings set with `textContent`,
 * and the app ships no runtime dependency except `nostr-tools`. A dictionary
 * and a `t()` cover it.
 *
 * Scope, on purpose:
 *  - The **agent** (CLI output, errors, logs) is English only. A translated
 *    log is a log you cannot grep or paste into an issue.
 *  - The **Service Worker** is English only. It is a separate build target
 *    with no DOM and no idea which language the page picked; its four
 *    plain-text bodies are rare failure paths, and navigations already land
 *    on this translated screen instead.
 *  - Thrown `Error` messages from modules stay English and are shown
 *    verbatim. They are developer-facing and only surface on rare paths.
 */

export type Lang = "en" | "tr";

export const DEFAULT_LANG: Lang = "en";
export const LANGS: readonly Lang[] = ["en", "tr"];

/** Key under which the chosen language is remembered. */
export const LANG_STORAGE_KEY = "peerpages-lang";

type Dict = Record<string, string>;

const en: Dict = {
  "app.tagline": "Serverless tunnel",

  "lang.label": "Language",
  "lang.en": "English",
  "lang.tr": "Türkçe",

  "step.sw": "Service Worker",
  "step.signal": "Signaling",
  "step.ice": "ICE",
  "step.tunnel": "Tunnel",

  "status.booting": "Starting…",
  "status.noServiceWorker":
    "This browser has no Service Worker support — the tunnel cannot be set up.",
  "status.swInstalling": "Installing the Service Worker…",
  "status.swActive": "Service Worker active. Paste the agent's offer.",
  "status.swFailed": "Service Worker registration failed: {err}",
  "status.channelsOpening": "Opening channels, gathering ICE candidates…",
  "status.offerReady":
    "Offer ready — paste it into the agent, then bring the agent's answer back here.",
  "status.tunnelOpen": "Tunnel open. Keep this tab open.",
  "status.tunnelFailed": "Tunnel could not be opened: {err}",
  "status.offerFailed": "Could not create an offer: {err}",
  "status.makeOfferFirst": "Create an offer first.",
  "status.answerUnreadable": "Could not read the answer: {err}",
  "status.answerExpected": "Expected an answer, got: {type}",
  "status.answerApplied": "Answer applied, waiting for the channels…",
  "status.tunnelClosed": "Tunnel closed ({state}). You can create a new offer.",
  "status.offerCopied": "Offer copied — paste it into the agent.",
  "status.copyFailed": "Could not copy; select the text and copy it by hand.",
  "status.pasteAnswerFirst": "Paste the answer the agent printed first.",
  "status.badPeerID": "The peer-id in the URL is not valid.",
  "status.relaysConnecting": "Connecting to the relays…",
  "status.relaysConnectingDetail": "Connecting to the relays ({detail})…",
  "status.offerPublishing": "Publishing the connection offer…",
  "status.waitingAgent": "Waiting for the agent to answer…",
  "status.iceConnecting": "Establishing a direct connection (ICE)…",
  "status.noPeerID": "No peer-id in the URL — see below.",
  "status.reconnectIn": "Connection lost{reason} — retrying in {seconds} s…",
  "status.reconnectAttempt": "Connection lost{reason} — reconnecting (attempt {attempt})…",

  "reason.noPong": "no response",

  "panel.nostr.title": "Connecting to the agent",
  "panel.nostr.peerid": "peer-id:",
  "btn.retry": "Try again",
  "btn.retryNow": "Try now",

  "intro.title": "What is this?",
  "intro.lead":
    "lets you share a web server running on your own machine — localhost:3000 — " +
    "with anyone, over this GitHub Pages address. No proxy server, no account, " +
    "nothing for the visitor to install.",
  "intro.sent.q": "Someone sent you a link?",
  "intro.sent.a":
    "It needs a peer-id: the address must end with #npub1… Ask them to resend the " +
    "full URL their agent printed.",
  "intro.share.q": "Want to share your own localhost?",
  "intro.share.a":
    "Install the agent and run it — it prints a ready-to-share link back to this page:",
  "intro.docs": "How it works, the security model and the honest limits:",

  "manual.step1.title": "1. Create an offer and paste it into the agent",
  "manual.step1.hint.before": "Run the agent:",
  "manual.step1.hint.after": "— then create an offer here and paste it into the agent's terminal.",
  "manual.offerPlaceholder": "(appears here once you press Create offer)",
  "manual.step2.title": "2. Paste the agent's answer",
  "btn.makeOffer": "Create offer",
  "btn.copy": "Copy",
  "btn.applyAnswer": "Apply answer",

  "connected.title": "Tunnel open",
  "connected.keepOpen.strong": "Keep this tab open",
  "connected.keepOpen.rest":
    "— this page holds the WebRTC connection and the Service Worker forwards requests here.",
  "connected.openSite.before": "Open the site in a",
  "connected.openSite.strong": "new tab",
  "connected.openSite.link": "open through the tunnel",
  "share.hint": "Open this address on another device — it joins the same tunnel:",
  "btn.copyUrl": "Copy address",
  "btn.qr": "QR code",
  "qr.ariaLabel": "QR code of the visitor address",
  "qr.hint": "Scan it with your phone's camera.",
  "qr.failed": "Could not render the QR code: {err}",
  "copy.done": "Copied",
  "copy.failed": "Could not copy — select it by hand",

  "metrics.latency": "Latency",
  "metrics.requests": "Requests",
  "metrics.received": "Received",
  "metrics.sent": "Sent",
  "metrics.rate": "Rate",
  "metrics.inflight": "In flight",
  "metrics.targets": "Targets",
  "metrics.activeTarget": "Active target",
  "metrics.route": "Connection path",
  "metrics.reconnects": "Reconnects",
  "metrics.state": "State",
  "metrics.websocket": "WebSocket",
  "metrics.measuring": "measuring…",
  "metrics.latencyValue": "{last} ms (median {median}, n={n})",
  "metrics.errors": "{n} failed",
  "metrics.stateReconnecting": "connection lost, retrying",
  "metrics.wsOpen": "{n} open",
  "metrics.none": "—",

  "route.host": "direct (local network)",
  "route.srflx": "direct (through NAT)",
  "route.prflx": "direct (peer-reflexive)",
  "route.relay": "through TURN (relay)",
  "route.unknown": "measuring…",

  "boot.preparing": "Preparing PeerPages…",
  noscript: "PeerPages needs JavaScript.",
};

const tr: Dict = {
  "app.tagline": "Sunucusuz tünel",

  "lang.label": "Dil",
  "lang.en": "English",
  "lang.tr": "Türkçe",

  "step.sw": "Service Worker",
  "step.signal": "Signaling",
  "step.ice": "ICE",
  "step.tunnel": "Tünel",

  "status.booting": "Başlatılıyor…",
  "status.noServiceWorker": "Bu tarayıcı Service Worker desteklemiyor — tünel kurulamaz.",
  "status.swInstalling": "Service Worker kuruluyor…",
  "status.swActive": "Service Worker aktif. Ajanın offer'ını yapıştır.",
  "status.swFailed": "Service Worker kaydı başarısız: {err}",
  "status.channelsOpening": "Kanallar açılıyor, ICE adayları toplanıyor…",
  "status.offerReady": "Offer hazır — ajana yapıştır, sonra ajanın answer'ını buraya getir.",
  "status.tunnelOpen": "Tünel açık. Bu sekmeyi kapatma.",
  "status.tunnelFailed": "Tünel kurulamadı: {err}",
  "status.offerFailed": "Offer üretilemedi: {err}",
  "status.makeOfferFirst": "Önce offer üret.",
  "status.answerUnreadable": "Answer okunamadı: {err}",
  "status.answerExpected": "Answer bekleniyordu, gelen: {type}",
  "status.answerApplied": "Answer uygulandı, kanallar bekleniyor…",
  "status.tunnelClosed": "Tünel kapandı ({state}). Yeniden offer üretebilirsin.",
  "status.offerCopied": "Offer kopyalandı — ajana yapıştır.",
  "status.copyFailed": "Kopyalanamadı; metni elle seç ve kopyala.",
  "status.pasteAnswerFirst": "Önce ajanın bastığı answer'ı yapıştır.",
  "status.badPeerID": "URL'deki peer-id geçersiz.",
  "status.relaysConnecting": "Relay'lere bağlanılıyor…",
  "status.relaysConnectingDetail": "Relay'lere bağlanılıyor ({detail})…",
  "status.offerPublishing": "Bağlantı teklifi yayınlanıyor…",
  "status.waitingAgent": "Ajanın yanıtı bekleniyor…",
  "status.iceConnecting": "Doğrudan bağlantı kuruluyor (ICE)…",
  "status.noPeerID": "URL'de peer-id yok — aşağıya bak.",
  "status.reconnectIn": "Bağlantı koptu{reason} — {seconds} sn içinde yeniden deneniyor…",
  "status.reconnectAttempt": "Bağlantı koptu{reason} — yeniden bağlanılıyor ({attempt}. deneme)…",

  "reason.noPong": "yanıt yok",

  "panel.nostr.title": "Ajana bağlanılıyor",
  "panel.nostr.peerid": "peer-id:",
  "btn.retry": "Yeniden dene",
  "btn.retryNow": "Şimdi dene",

  "intro.title": "Bu nedir?",
  "intro.lead":
    "kendi makinende çalışan bir web sunucusunu — localhost:3000 — bu GitHub Pages " +
    "adresi üzerinden herkesle paylaşmanı sağlar. Arada proxy sunucu yok, hesap yok, " +
    "ziyaretçinin kuracağı bir şey yok.",
  "intro.sent.q": "Biri sana bir bağlantı mı yolladı?",
  "intro.sent.a":
    "Adresin peer-id taşıması gerekir: sonu #npub1… ile bitmeli. Ajanının bastığı tam " +
    "adresi yeniden istemelisin.",
  "intro.share.q": "Kendi localhost'unu mu paylaşmak istiyorsun?",
  "intro.share.a": "Ajanı kur ve çalıştır — paylaşmaya hazır bir bağlantıyı bu sayfaya geri basar:",
  "intro.docs": "Nasıl çalıştığı, güvenlik modeli ve dürüst sınırlar:",

  "manual.step1.title": "1. Offer üret ve ajana yapıştır",
  "manual.step1.hint.before": "Ajanı çalıştır:",
  "manual.step1.hint.after": "— sonra burada offer üret ve ajanın terminaline yapıştır.",
  "manual.offerPlaceholder": "(Offer üret'e basınca burada oluşur)",
  "manual.step2.title": "2. Ajanın answer'ını yapıştır",
  "btn.makeOffer": "Offer üret",
  "btn.copy": "Kopyala",
  "btn.applyAnswer": "Answer'ı uygula",

  "connected.title": "Tünel açık",
  "connected.keepOpen.strong": "Bu sekmeyi kapatma",
  "connected.keepOpen.rest":
    "— WebRTC bağlantısını bu sayfa tutuyor ve Service Worker istekleri buraya yönlendiriyor.",
  "connected.openSite.before": "Siteyi",
  "connected.openSite.strong": "yeni bir sekmede",
  "connected.openSite.link": "tünel üzerinden aç",
  "share.hint": "Bu adresi başka bir cihazda aç — aynı tünele bağlanır:",
  "btn.copyUrl": "Adresi kopyala",
  "btn.qr": "QR kod",
  "qr.ariaLabel": "Ziyaretçi adresinin QR kodu",
  "qr.hint": "Telefonun kamerasıyla okut.",
  "qr.failed": "QR üretilemedi: {err}",
  "copy.done": "Kopyalandı",
  "copy.failed": "Kopyalanamadı — elle seç",

  "metrics.latency": "Gecikme",
  "metrics.requests": "İstek",
  "metrics.received": "Alınan",
  "metrics.sent": "Gönderilen",
  "metrics.rate": "Hız",
  "metrics.inflight": "Devam eden",
  "metrics.targets": "Hedefler",
  "metrics.activeTarget": "Aktif hedef",
  "metrics.route": "Bağlantı yolu",
  "metrics.reconnects": "Yeniden bağlanma",
  "metrics.state": "Durum",
  "metrics.websocket": "WebSocket",
  "metrics.measuring": "ölçülüyor…",
  "metrics.latencyValue": "{last} ms (medyan {median}, n={n})",
  "metrics.errors": "{n} hata",
  "metrics.stateReconnecting": "bağlantı koptu, yeniden deneniyor",
  "metrics.wsOpen": "{n} açık",
  "metrics.none": "—",

  "route.host": "doğrudan (yerel ağ)",
  "route.srflx": "doğrudan (NAT üzerinden)",
  "route.prflx": "doğrudan (peer-reflexive)",
  "route.relay": "TURN üzerinden (relay)",
  "route.unknown": "ölçülüyor…",

  "boot.preparing": "PeerPages hazırlanıyor…",
  noscript: "PeerPages JavaScript gerektirir.",
};

const dict: Record<Lang, Dict> = { en, tr };

let current: Lang = DEFAULT_LANG;

/** The language in use right now. */
export function lang(): Lang {
  return current;
}

function isLang(v: string | null | undefined): v is Lang {
  return v === "en" || v === "tr";
}

/**
 * Picks a language from, in order: `?lang=`, the stored choice, the browser
 * list, then the default.
 *
 * Unknown values are IGNORED rather than rejected. This departs on purpose
 * from `relaysFromSearch` / `stunFromSearch`, which throw on bad input: a
 * dropped relay list silently breaks the tunnel, so the user must hear about
 * it, while a language we do not have is cosmetic — failing the page over it
 * would be out of proportion.
 */
export function resolveLang(
  search: string,
  stored: string | null,
  navLangs: readonly string[] = [],
): Lang {
  const fromQuery = new URLSearchParams(search).get("lang");
  if (isLang(fromQuery)) return fromQuery;
  if (fromQuery) {
    console.warn(`[peerpages] unknown ?lang=${fromQuery}, falling back to ${DEFAULT_LANG}`);
  }

  if (isLang(stored)) return stored;

  for (const raw of navLangs) {
    // "tr-TR" and "tr" both mean Turkish.
    const base = raw.toLowerCase().split("-")[0];
    if (isLang(base)) return base;
  }
  return DEFAULT_LANG;
}

/**
 * Looks up a key and fills `{placeholders}`.
 *
 * A missing key returns the key itself: a visible, searchable breadcrumb
 * instead of an empty element. The real guard is the unit test asserting
 * both dictionaries carry the same key set.
 */
export function t(key: string, params?: Record<string, string | number>): string {
  const table = dict[current];
  const fallback = dict[DEFAULT_LANG];
  const raw = table[key] ?? fallback[key];
  if (raw === undefined) {
    console.warn(`[peerpages] missing translation: ${key}`);
    return key;
  }
  if (!params) return raw;
  return raw.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in params ? String(params[name]) : whole,
  );
}

/**
 * Sets the language, remembers it and updates `<html lang>`.
 *
 * Storage can throw (private mode, blocked site data), so it never fails the
 * caller — the page still switches, it just will not remember.
 */
export function setLang(next: Lang, doc?: Document): void {
  current = next;
  // Node (unit tests) has no document; the pure part still works there.
  const target = doc ?? (typeof document === "undefined" ? null : document);
  if (target) target.documentElement.lang = next;
  try {
    localStorage.setItem(LANG_STORAGE_KEY, next);
  } catch {
    // Not fatal: the choice simply does not survive a reload.
  }
}

/** Reads the stored choice; returns null when storage is unavailable. */
export function storedLang(): string | null {
  try {
    return localStorage.getItem(LANG_STORAGE_KEY);
  } catch {
    return null;
  }
}

/**
 * Applies the dictionary to the DOM.
 *
 * `data-i18n="key"` sets the element's text; `data-i18n-attr="attr:key,…"`
 * sets attributes (placeholder, aria-label). Only `textContent` is written —
 * the app never uses `innerHTML`, so a translation cannot inject markup.
 */
export function applyTranslations(root: ParentNode): void {
  for (const el of root.querySelectorAll<HTMLElement>("[data-i18n]")) {
    const key = el.dataset.i18n;
    if (key) el.textContent = t(key);
  }
  for (const el of root.querySelectorAll<HTMLElement>("[data-i18n-attr]")) {
    const spec = el.dataset.i18nAttr;
    if (!spec) continue;
    for (const pair of spec.split(",")) {
      const [attr, key] = pair.split(":").map((s) => s.trim());
      if (attr && key) el.setAttribute(attr, t(key));
    }
  }
}

/** Keys of a dictionary, for the "both languages agree" test. */
export function keysOf(l: Lang): string[] {
  return Object.keys(dict[l]).sort();
}
