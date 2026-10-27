import "./style.css";
import { BASE_PATH, inBase } from "./base";
import {
  IceConfigError,
  type IceRoute,
  iceServers,
  routeFromStats,
  routeLabel,
  stunFromSearch,
} from "./ice";
import { type ConnectResult, connectToAgent } from "./nostr/connect";
import { PeerIDError, type PeerTarget, parseHash, shortenNpub } from "./nostr/peerid";
import { RelayListError, relaysFromSearch } from "./nostr/relays";
import {
  announceTunnelGone,
  announceTunnelReady,
  announceTunnelReconnecting,
  installBridge,
} from "./page-bridge";
import { Supervisor, type SupervisorInfo, type SupervisorState } from "./reconnect";
import { decodeSDP, encodeSDP } from "./signal";
import { Tunnel } from "./tunnel";
import { encode, type QRCode } from "./ui/qr";
import { StatusView } from "./ui/status";

/**
 * Baglanti ekrani ve tunel sahibi sayfa.
 *
 * Iki yol var:
 *  - **Nostr (varsayilan):** URL'de peer-id varsa (`#npub1…`) baglanti
 *    kendiliginden kurulur; kopyala-yapistir yok.
 *  - **Elle (`--manual`):** URL'de peer-id yoksa bu sayfa offer uretir,
 *    kullanici ajana yapistirir, ajanin answer'ini geri getirir.
 *
 * Rol degisimi (Faz 2): kanallari (`ctrl`/`data`) ve offer'i ziyaretci
 * uretir, ajan answer doner. Nedeni: Nostr'da ajan ziyaretcinin gecici
 * pubkey'ini onceden bilemez, ilk mesaji o gonderemez. Karar ve gerekcesi
 * BRIEF bolum 8'de.
 *
 * WebRTC baglantisini BU sayfa tutar; Service Worker icinde WebRTC yok.
 * Sayfa kapanirsa tunel kapanir.
 */

const status = new StatusView();

let tunnel: Tunnel | null = null;
let peer: RTCPeerConnection | null = null;
let nostrSession: ConnectResult | null = null;
/** URL'den gelen hedef adi; yoksa null (ajan default'a duser). */
let activeTarget: string | null = null;

// Olcum ve durum ekrani icin sayaclar.
const counters = { requests: 0, errors: 0, bytes: 0 };

/**
 * Gecikme olcumu artik SUREKLI: baglanma aninda bir kez degil, duzenli
 * araliklarla. Tek bir ilk olcum tuneli acikken ne oldugunu anlatmiyor.
 */
const LATENCY_INTERVAL_MS = 5000;
const LATENCY_HISTORY = 12;
const latencies: number[] = [];
let latencyTimer: number | null = null;

/** Hiz hesabi icin son ornek (tunnel.stats zaman damgasi tutmuyor). */
let lastSample: { at: number; bytesIn: number; bytesOut: number } | null = null;
let rateBps = 0;

/**
 * Yeniden baglanma denetleyicisi (Faz 4); yalnizca Nostr yolunda var.
 *
 * Elle signaling yolunda otomatik yeniden baglanma mumkun degil: offer'i
 * kullanici tasiyor.
 */
let supervisor: Supervisor | null = null;
/** Yeniden baglanirken kullanilacak hedef ve relay listesi. */
let reconnectTarget: PeerTarget | null = null;
let reconnectRelays: string[] | null = null;
/** URL'den gelen STUN listesi; null ise varsayilan. */
let stunOverride: string[] | null = null;
/** Baglantinin hangi yoldan kuruldugu (TURN kullanilip kullanilmadigi). */
let iceRoute: IceRoute = "bilinmiyor";
/** Acik ws oturumu sayisi (Faz 6). */
let openSockets = 0;
/** Kendi kapatma islemimiz sirasinda durum olaylarini yok saymak icin. */
let tearingDown = false;

/**
 * Ust uste bu kadar basarisiz ping'den sonra tunel kopmus sayilir.
 *
 * Neden gerekli: WebRTC durum makinesi her kopmayi bildirmiyor — kanallar
 * "open" gorunurken karsi taraf gitmis olabilir. Ping zaten 5 saniyede bir
 * atiliyor, yani bu ucuz bir canlilik denetimi.
 */
const PING_FAILURES_LOST = 2;
let pingFailures = 0;

const el = {
  offerOutput: document.getElementById("offer-output") as HTMLTextAreaElement | null,
  makeOfferBtn: document.getElementById("btn-make-offer") as HTMLButtonElement | null,
  copyOfferBtn: document.getElementById("btn-copy-offer") as HTMLButtonElement | null,
  answerInput: document.getElementById("answer-input") as HTMLTextAreaElement | null,
  acceptAnswerBtn: document.getElementById("btn-accept-answer") as HTMLButtonElement | null,
  panelManual: document.getElementById("panel-manual"),
  panelNostr: document.getElementById("panel-nostr"),
  nostrPeer: document.getElementById("nostr-peer"),
  nostrDetail: document.getElementById("nostr-detail"),
  retryBtn: document.getElementById("btn-retry") as HTMLButtonElement | null,
  panelConnected: document.getElementById("panel-connected"),
  metrics: document.getElementById("metrics"),
  shareUrl: document.getElementById("share-url"),
  copyUrlBtn: document.getElementById("btn-copy-url") as HTMLButtonElement | null,
  qrBtn: document.getElementById("btn-qr") as HTMLButtonElement | null,
  qrBox: document.getElementById("qr"),
  qrCanvas: document.getElementById("qr-canvas") as HTMLCanvasElement | null,
  linkSite: document.getElementById("link-site") as HTMLAnchorElement | null,
};

installBridge(() => tunnel, {
  onRequestDone: ({ bytes }) => {
    counters.requests += 1;
    counters.bytes += bytes;
    renderMetrics();
  },
  onRequestError: ({ path, message }) => {
    counters.errors += 1;
    renderMetrics();
    console.warn(`[peerpages] istek basarisiz: ${path} — ${message}`);
  },
  onSocketChange: (open) => {
    openSockets = open;
    renderMetrics();
  },
});

async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) {
    status.step("sw", "error");
    status.say("Bu tarayici Service Worker desteklemiyor — tunel kurulamaz.", "error");
    return null;
  }

  status.step("sw", "active");
  status.say("Service Worker kuruluyor…");

  try {
    // Scope BASE_PATH ile sinirli: SW yalnizca kendi kapsamindaki istekleri
    // yakalar, baska origin'lere ya da ust yollara dokunmaz (ilke 5).
    const reg = await navigator.serviceWorker.register(inBase("sw.js"), { scope: BASE_PATH });
    await navigator.serviceWorker.ready;

    // Ilk yuklemede sayfa henuz kontrol edilmiyor olabilir; controller
    // gelmeden SW'ye mesaj yollayamayiz.
    if (!navigator.serviceWorker.controller) {
      await new Promise<void>((resolve) => {
        const onChange = () => {
          navigator.serviceWorker.removeEventListener("controllerchange", onChange);
          resolve();
        };
        navigator.serviceWorker.addEventListener("controllerchange", onChange);
        // Bazi durumlarda controllerchange gelmez (zaten aktif SW var):
        // kisa bir sure sonra devam et.
        setTimeout(resolve, 1500);
      });
    }

    status.step("sw", "done");
    status.say("Service Worker aktif. Ajanin offer'ini yapistir.");
    return reg;
  } catch (err) {
    status.step("sw", "error");
    status.say(`Service Worker kaydi basarisiz: ${String(err)}`, "error");
    return null;
  }
}

/**
 * Oturumu baslatir: kanallari acar, offer uretir ve ekrana basar.
 *
 * Kanallari BU TARAF aciyor (rol degisimi): `ctrl` ve `data`, ikisi de
 * ordered + reliable (protocol/README.md).
 */
async function makeOffer(): Promise<void> {
  status.step("signal", "active");
  status.say("Kanallar aciliyor, ICE adaylari toplaniyor…");

  const pc = new RTCPeerConnection();
  peer = pc;

  const ctrl = pc.createDataChannel("ctrl", { ordered: true });
  const data = pc.createDataChannel("data", { ordered: true });

  const bothOpen = new Promise<void>((resolve, reject) => {
    const check = () => {
      if (ctrl.readyState === "open" && data.readyState === "open") {
        resolve();
      }
    };
    ctrl.addEventListener("open", check);
    data.addEventListener("open", check);
    setTimeout(() => reject(new Error("kanallar 60 sn icinde acilmadi")), 60_000);
  });

  pc.onconnectionstatechange = () => {
    const state = pc.connectionState;
    if (state === "connected") {
      status.step("ice", "done");
    } else if (state === "failed" || state === "closed" || state === "disconnected") {
      onTunnelLost(state);
    }
  };

  try {
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    status.step("ice", "active");
    await waitForIceGathering(pc);

    const local = pc.localDescription;
    if (!local) throw new Error("yerel SDP olusturulamadi");

    const encoded = encodeSDP({ type: local.type, sdp: local.sdp });
    if (el.offerOutput) {
      el.offerOutput.value = encoded;
      el.offerOutput.select();
    }
    if (el.copyOfferBtn) el.copyOfferBtn.disabled = false;
    if (el.acceptAnswerBtn) el.acceptAnswerBtn.disabled = false;

    status.step("signal", "done");
    status.say("Offer hazir — ajana yapistir, sonra ajanin answer'ini buraya getir.");

    // Kanallar acilinca tuneli kur.
    void bothOpen
      .then(() => {
        tunnel = new Tunnel(ctrl, data);
        announceTunnelReady();
        status.step("tunnel", "done");
        status.say("Tunel acik. Bu sekmeyi kapatma.", "ok");
        showConnected();
      })
      .catch((err) => {
        status.step("tunnel", "error");
        status.say(
          `Tunel kurulamadi: ${err instanceof Error ? err.message : String(err)}`,
          "error",
        );
      });
  } catch (err) {
    status.step("signal", "error");
    status.say(`Offer uretilemedi: ${err instanceof Error ? err.message : String(err)}`, "error");
    pc.close();
    peer = null;
  }
}

/** Ajanin bastigi answer'i uygular. */
async function acceptAnswer(pasted: string): Promise<void> {
  const pc = peer;
  if (!pc) {
    status.say("Once offer uret.", "error");
    return;
  }

  let answer: RTCSessionDescriptionInit;
  try {
    answer = decodeSDP(pasted);
  } catch (err) {
    status.say(`Answer okunamadi: ${err instanceof Error ? err.message : String(err)}`, "error");
    return;
  }
  if (answer.type !== "answer") {
    status.say(`Answer bekleniyordu, gelen: ${String(answer.type)}`, "error");
    return;
  }

  try {
    await pc.setRemoteDescription(answer);
    status.step("tunnel", "active");
    status.say("Answer uygulandi, kanallar bekleniyor…");
  } catch (err) {
    status.step("tunnel", "error");
    status.say(`Answer uygulanamadi: ${err instanceof Error ? err.message : String(err)}`, "error");
  }
}

function waitForIceGathering(pc: RTCPeerConnection): Promise<void> {
  if (pc.iceGatheringState === "complete") return Promise.resolve();
  return new Promise<void>((resolve) => {
    const check = () => {
      if (pc.iceGatheringState === "complete") {
        pc.removeEventListener("icegatheringstatechange", check);
        resolve();
      }
    };
    pc.addEventListener("icegatheringstatechange", check);
    // Trickle yok ama bazi tarayicilar "complete"a gecmekte gecikebilir.
    setTimeout(resolve, 10_000);
  });
}

/** Acik tuneli, oturumu ve zamanlayicilari birakir. */
function teardownTunnel(reason: string): void {
  tearingDown = true;
  try {
    stopLatencyProbe();
    pingFailures = 0;
    if (tunnel) {
      tunnel.close();
      tunnel = null;
      announceTunnelGone();
    }
    if (peer) {
      peer.close();
      peer = null;
    }
    if (nostrSession) {
      nostrSession.close(reason);
      nostrSession = null;
    }
  } finally {
    tearingDown = false;
  }
}

/**
 * Tunel koptu.
 *
 * Nostr yolunda denetleyici devralir: SIFIRDAN yeni bir oturum kurulur
 * (ajan answerer-only oldugu icin baska yolu yok). Elle signaling yolunda
 * otomatik yeniden baglanma mumkun degil, eski davranis korunuyor.
 */
function onTunnelLost(state: string): void {
  if (tearingDown) return;
  teardownTunnel(`connection-${state}`);

  if (supervisor) {
    status.step("tunnel", "active");
    // SW'ye haber ver: bu boslukta gelen istekler 503 yerine kisa sure
    // beklesin.
    announceTunnelReconnecting();
    supervisor.markLost(state);
    return;
  }

  status.step("tunnel", "error");
  status.say(`Tunel kapandi (${state}). Yeniden offer uretebilirsin.`, "error");
  if (el.panelConnected) el.panelConnected.hidden = true;
  if (el.panelManual) el.panelManual.hidden = false;
  if (el.makeOfferBtn) el.makeOfferBtn.disabled = false;
}

/** Denetleyicinin durum degisimlerini ekrana yazar. */
function renderReconnectState(state: SupervisorState, info: SupervisorInfo): void {
  if (el.retryBtn) {
    el.retryBtn.textContent = "Simdi dene";
    el.retryBtn.disabled = state !== "reconnecting";
  }
  if (state !== "reconnecting") {
    renderMetrics();
    return;
  }

  // Panel acik kalir: sayaclar ve adres gorunur olsun, ziyaretci neyin
  // devam ettigini bilsin.
  const neden = info.reason ? ` (${info.reason})` : "";
  const msg =
    info.waitMs > 0
      ? `Baglanti koptu${neden} — ${Math.round(info.waitMs / 1000)} sn icinde yeniden deneniyor…`
      : `Baglanti koptu${neden} — yeniden baglaniliyor (${info.attempt}. deneme)…`;
  status.say(msg, "error");
  renderMetrics();
}

function showConnected(): void {
  if (el.panelManual) el.panelManual.hidden = true;
  if (el.panelConnected) el.panelConnected.hidden = false;
  // Yerel sitenin koku scope kokunde; bu sayfa connect/ altinda duruyor.
  if (el.linkSite) el.linkSite.href = BASE_PATH;
  // Paylasim bloku yalnizca Nostr yolunda anlamli: elle yolda URL'de
  // peer-id yok, yani adresi baska cihaza tasimanin faydasi da yok.
  const shareable = location.hash.trim().length > 1;
  const shareBox = el.shareUrl?.closest(".share") as HTMLElement | null;
  if (shareBox) shareBox.hidden = !shareable;
  if (el.shareUrl) el.shareUrl.textContent = location.href;
  renderMetrics();
  startLatencyProbe();
}

function startLatencyProbe(): void {
  void measurePing();
  if (latencyTimer !== null) return;
  latencyTimer = window.setInterval(() => {
    void measurePing();
    renderMetrics();
  }, LATENCY_INTERVAL_MS);
}

function stopLatencyProbe(): void {
  if (latencyTimer !== null) {
    window.clearInterval(latencyTimer);
    latencyTimer = null;
  }
}

async function measurePing(): Promise<void> {
  if (!tunnel) return;
  void refreshIceRoute();
  try {
    const rtt = await tunnel.ping();
    latencies.push(rtt);
    if (latencies.length > LATENCY_HISTORY) latencies.shift();
    pingFailures = 0;
  } catch {
    // Tek bir basarisizlik yeterli degil: pong gecikmis olabilir. Ust uste
    // gelirse karsi taraf gitmis demektir — kanallar hala "open" gorunse
    // bile.
    pingFailures += 1;
    if (pingFailures >= PING_FAILURES_LOST) {
      onTunnelLost("ping-yok");
      return;
    }
  }
  renderMetrics();
}

/**
 * Secilen aday ciftini okuyup baglanti yolunu tazeler.
 *
 * Tunel acildiktan HEMEN sonra bos gelebiliyor (tarayici aday ciftini
 * yayinlamamis olabilir), bu yuzden gecikme tikinde tekrar bakiyoruz.
 */
async function refreshIceRoute(): Promise<void> {
  const pc = nostrSession?.pc ?? peer;
  if (!pc) return;
  try {
    const stats = await pc.getStats();
    const route = routeFromStats(stats);
    if (route !== iceRoute) {
      iceRoute = route;
      renderMetrics();
    }
  } catch {
    // getStats desteklenmiyorsa ya da baglanti kapandiysa onemli degil.
  }
}

function median(values: readonly number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) return sorted[mid] ?? 0;
  return ((sorted[mid - 1] ?? 0) + (sorted[mid] ?? 0)) / 2;
}

/** Anlik hiz: son ornekten bu yana gecen bayt / gecen sure. */
function sampleRate(bytesIn: number, bytesOut: number): number {
  const now = performance.now();
  if (!lastSample) {
    lastSample = { at: now, bytesIn, bytesOut };
    return 0;
  }
  const elapsed = (now - lastSample.at) / 1000;
  if (elapsed < 0.5) return rateBps;

  const delta = bytesIn - lastSample.bytesIn + (bytesOut - lastSample.bytesOut);
  lastSample = { at: now, bytesIn, bytesOut };
  rateBps = delta / elapsed;
  return rateBps;
}

function renderMetrics(): void {
  if (!el.metrics) return;
  const stats = tunnel?.stats;
  const rate = sampleRate(stats?.bytesIn ?? 0, stats?.bytesOut ?? 0);

  const latency =
    latencies.length === 0
      ? "olculuyor…"
      : `${(latencies[latencies.length - 1] ?? 0).toFixed(1)} ms ` +
        `(medyan ${median(latencies).toFixed(1)}, n=${latencies.length})`;

  const rows: Array<[string, string]> = [
    ["Gecikme", latency],
    ["Istek", `${counters.requests}${counters.errors > 0 ? ` (${counters.errors} hata)` : ""}`],
    ["Alinan", formatBytes(stats?.bytesIn ?? 0)],
    ["Gonderilen", formatBytes(stats?.bytesOut ?? 0)],
    ["Hiz", rate > 0 ? `${formatBytes(rate)}/s` : "—"],
    ["Devam eden", String(stats?.inflight ?? 0)],
    ["Hedefler", tunnel?.hello?.targets.join(", ") ?? "—"],
    ["Aktif hedef", activeTarget ?? "default"],
    // Trafik bir TURN sunucusundan geciyorsa ziyaretci bunu bilmeli:
    // ucten uca sifreleme surer ama YOL degisir.
    ["Baglanti yolu", routeLabel(iceRoute)],
  ];

  // Yeniden baglanma yalnizca olduysa gosterilir: sifir satiri gurultudur.
  if (supervisor && supervisor.reconnects > 0) {
    rows.push(["Yeniden baglanma", String(supervisor.reconnects)]);
  }
  if (supervisor?.currentState === "reconnecting") {
    rows.push(["Durum", "baglanti koptu, yeniden deneniyor"]);
  }
  // WebSocket satiri yalnizca oturum varsa: her zaman sifir okunan satir
  // gurultudur (Faz 6).
  if (openSockets > 0) {
    rows.push(["WebSocket", `${openSockets} acik`]);
  }

  el.metrics.textContent = "";
  for (const [label, value] of rows) {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value;
    el.metrics.append(dt, dd);
  }
}

function formatBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(2)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}

function wireUI(): void {
  el.makeOfferBtn?.addEventListener("click", () => {
    if (el.makeOfferBtn) el.makeOfferBtn.disabled = true;
    void makeOffer();
  });

  el.copyOfferBtn?.addEventListener("click", async () => {
    const value = el.offerOutput?.value ?? "";
    if (value === "") return;
    try {
      await navigator.clipboard.writeText(value);
      status.say("Offer kopyalandi — ajana yapistir.");
    } catch {
      el.offerOutput?.select();
      status.say("Kopyalanamadi; metni elle sec ve kopyala.", "error");
    }
  });

  el.acceptAnswerBtn?.addEventListener("click", () => {
    const pasted = el.answerInput?.value ?? "";
    if (pasted.trim() === "") {
      status.say("Once ajanin bastigi answer'i yapistir.", "error");
      return;
    }
    if (el.acceptAnswerBtn) el.acceptAnswerBtn.disabled = true;
    void acceptAnswer(pasted).finally(() => {
      if (el.acceptAnswerBtn) el.acceptAnswerBtn.disabled = false;
    });
  });

  el.copyUrlBtn?.addEventListener("click", () => {
    void copyVisitorURL();
  });

  el.qrBtn?.addEventListener("click", () => {
    toggleQR();
  });

  el.retryBtn?.addEventListener("click", () => {
    // Denetleyici zaten kuruluysa geri sayimi atlayip hemen dener.
    if (supervisor) {
      supervisor.retryNow();
      return;
    }
    const hash = location.hash;
    try {
      const target = parseHash(hash);
      if (target) void connectViaNostr(target);
    } catch {
      status.say("URL'deki peer-id gecersiz.", "error");
    }
  });

  // Sekme kapanirken SW'ye haber ver ve ajana `bye` yolla: ajan kaynak
  // sizdirmasin, bir sonraki istek bosa beklemesin.
  window.addEventListener("pagehide", () => {
    stopLatencyProbe();
    // Sayfa gidiyor: yeniden baglanmaya calismanin anlami yok.
    supervisor?.stop();
    if (tunnel) announceTunnelGone();
    if (nostrSession) {
      nostrSession.close("visitor-left");
      nostrSession = null;
    }
  });
}

/** URL'de peer-id varsa Nostr ile baglanir. */
async function connectViaNostr(target: PeerTarget): Promise<void> {
  let relayOverride: string[] | null = null;
  try {
    relayOverride = relaysFromSearch(location.search);
    stunOverride = stunFromSearch(location.search);
  } catch (err) {
    status.step("signal", "error");
    const known = err instanceof RelayListError || err instanceof IceConfigError;
    status.say(known ? (err as Error).message : String(err), "error");
    return;
  }

  activeTarget = target.target;
  if (el.panelManual) el.panelManual.hidden = true;
  if (el.panelNostr) el.panelNostr.hidden = false;
  // Hedef adi varsa ekranda da gorunsun: ziyaretci hangi projeye
  // baglandigini bilsin.
  if (el.nostrPeer) {
    el.nostrPeer.textContent = target.target
      ? `${shortenNpub(target.npub)} / ${target.target}`
      : shortenNpub(target.npub);
  }
  if (el.retryBtn) el.retryBtn.disabled = true;

  reconnectTarget = target;
  reconnectRelays = relayOverride;
  if (!supervisor) {
    supervisor = new Supervisor({
      connect: () => openNostrSession(),
      onState: renderReconnectState,
    });
    supervisor.start();
  }

  try {
    await openNostrSession();
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    status.step("tunnel", "error");
    status.say(msg, "error");
    // Ajan henuz ayakta olmayabilir: denetleyici denemeye devam eder,
    // ziyaretcinin elle yenilemesi gerekmez.
    supervisor.markLost(msg);
  }
}

/**
 * Tek bir Nostr oturumu acar.
 *
 * Her cagri SIFIRDAN kurar: yeni sid, yeni gecici keypair, yeni offer.
 * Ajan answerer-only ve oturum tek seferlik oldugu icin (ICE restart
 * yerine bu yol secildi, BRIEF §8) yeniden baglanma da budur.
 */
async function openNostrSession(): Promise<void> {
  const target = reconnectTarget;
  if (!target) throw new Error("hedef yok");
  const relayOverride = reconnectRelays;

  status.step("signal", "active");
  status.say("Relay'lere baglaniliyor…");

  const session = await connectToAgent({
    target,
    // BRIEF bolum 4: relay listesi kullanici tarafindan degistirilebilir
    // (?relay=wss://...). Verilmezse varsayilan liste.
    ...(relayOverride ? { relays: relayOverride } : {}),
    // STUN listesi de URL'den degistirilebilir (?stun=...); TURN kimlik
    // bilgisi sayfaya HIC gelmiyor (BRIEF §8).
    iceServers: iceServers(stunOverride),
    hooks: {
      onStage: (stage, detail) => {
        switch (stage) {
          case "relay":
            status.say(`Relay'lere baglaniliyor (${detail ?? ""})…`);
            break;
          case "offer":
            status.say("Baglanti teklifi yayinlaniyor…");
            break;
          case "answer":
            status.step("signal", "done");
            status.step("ice", "active");
            status.say("Ajanin yaniti bekleniyor…");
            break;
          case "ice":
            status.say("Dogrudan baglanti kuruluyor (ICE)…");
            break;
          case "open":
            status.step("ice", "done");
            break;
        }
        if (el.nostrDetail && detail) el.nostrDetail.textContent = detail;
      },
    },
  });

  nostrSession = session;
  status.step("tunnel", "active");

  session.pc.addEventListener("connectionstatechange", () => {
    const st = session.pc.connectionState;
    if (st === "failed" || st === "closed" || st === "disconnected") {
      onTunnelLost(st);
    }
  });

  // Yeni tunel: gecikme gecmisi eski oturuma ait, sifirlanir. Istek ve
  // bayt sayaclari KORUNUR — ziyaretci acisindan ayni oturum devam ediyor.
  latencies.length = 0;
  lastSample = null;
  pingFailures = 0;
  iceRoute = "bilinmiyor";

  tunnel = new Tunnel(session.ctrl, session.data);
  announceTunnelReady();
  status.step("tunnel", "done");
  status.say("Tunel acik. Bu sekmeyi kapatma.", "ok");
  showConnected();
  supervisor?.markConnected();
}

async function main(): Promise<void> {
  wireUI();
  const reg = await registerServiceWorker();
  if (!reg) return;

  let target: PeerTarget | null = null;
  try {
    target = parseHash(location.hash);
  } catch (err) {
    status.step("signal", "error");
    status.say(err instanceof PeerIDError ? err.message : String(err), "error");
    // Hatali peer-id: elle yola dusmek yerine sorunu gosteriyoruz, aksi
    // halde kullanici yanlis URL'yi fark etmeden elle ugrasmaya baslar.
    if (el.panelManual) el.panelManual.hidden = true;
    return;
  }

  if (target) {
    await connectViaNostr(target);
    return;
  }

  // peer-id yok: elle signaling yolu.
  status.step("signal", "active");
  status.say("URL'de peer-id yok — elle signaling yolu. Offer uret.");
  if (el.panelNostr) el.panelNostr.hidden = true;
}

void main();

// E2E testinin sayfa durumunu okuyabilmesi icin kucuk bir pencere kancasi.
// Uretim davranisini etkilemez.
declare global {
  interface Window {
    __peerpages?: {
      hasTunnel: () => boolean;
      stats: () => unknown;
      ping: () => Promise<number>;
      makeOffer: () => Promise<void>;
      offer: () => string;
      acceptAnswer: (pasted: string) => Promise<void>;
      reconnects: () => number;
      connState: () => string;
      iceRoute: () => string;
      /** Acik WebSocket oturumu sayisi (Faz 6). */
      wsSessions: () => number;
    };
  }
}

window.__peerpages = {
  hasTunnel: () => tunnel?.isOpen === true,
  stats: () => ({ ...counters, tunnel: tunnel?.stats ?? null }),
  reconnects: () => supervisor?.reconnects ?? 0,
  connState: () => supervisor?.currentState ?? "idle",
  iceRoute: () => iceRoute,
  wsSessions: () => tunnel?.socketCount ?? 0,
  ping: async () => (tunnel ? tunnel.ping() : Promise.reject(new Error("tunel yok"))),
  makeOffer: () => makeOffer(),
  offer: () => el.offerOutput?.value ?? "",
  acceptAnswer: (pasted: string) => acceptAnswer(pasted),
};

/**
 * Ziyaretci adresini panoya kopyalar.
 *
 * Yedek yol gercekten gerekli: `navigator.clipboard` GUVENLI BAGLAM ister ve
 * iki cihazla denemede tam olarak o durum var — telefon laptop'a duz IP
 * adresiyle baglandiginda clipboard API yok (docs/MANUAL-TEST.md).
 */
async function copyVisitorURL(): Promise<void> {
  const url = location.href;
  const done = (ok: boolean) => {
    if (!el.copyUrlBtn) return;
    const label = el.copyUrlBtn.textContent ?? "";
    el.copyUrlBtn.textContent = ok ? "Kopyalandi" : "Kopyalanamadi — elle sec";
    window.setTimeout(() => {
      if (el.copyUrlBtn) el.copyUrlBtn.textContent = label;
    }, 2000);
  };

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(url);
      done(true);
      return;
    }
  } catch {
    // Izin verilmedi ya da guvenli baglam degil: yedek yola gec.
  }

  // Yedek: gecici bir textarea uzerinden execCommand.
  try {
    const ta = document.createElement("textarea");
    ta.value = url;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.append(ta);
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    done(ok);
  } catch {
    done(false);
  }
}

/** QR panelini acar/kapatir; ilk acilista kodu uretir. */
function toggleQR(): void {
  if (!el.qrBox || !el.qrBtn) return;
  const show = el.qrBox.hidden;
  el.qrBox.hidden = !show;
  el.qrBtn.setAttribute("aria-expanded", String(show));
  if (show) drawQR(location.href);
}

/** QR kodunu canvas'a cizer. */
function drawQR(text: string): void {
  const canvas = el.qrCanvas;
  if (!canvas) return;

  let code: QRCode;
  try {
    code = encode(text);
  } catch (err) {
    // Adres cok uzunsa (ornegin uzun bir sir) QR uretilemez; kopyala butonu
    // calismaya devam ediyor, bu yuzden sadece sebebi yaziyoruz.
    const hint = el.qrBox?.querySelector(".qr__hint");
    if (hint) {
      hint.textContent = `QR uretilemedi: ${err instanceof Error ? err.message : String(err)}`;
    }
    return;
  }

  // Modul basina 4 piksel: telefon kamerasinin rahat okudugu boyut.
  const scale = 4;
  const quiet = 4; // standardin istedigi sessiz bolge (modul)
  const side = (code.size + quiet * 2) * scale;
  canvas.width = side;
  canvas.height = side;

  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, side, side);
  ctx.fillStyle = "#000";
  for (let r = 0; r < code.size; r++) {
    for (let c = 0; c < code.size; c++) {
      if (code.modules[r]?.[c]) {
        ctx.fillRect((c + quiet) * scale, (r + quiet) * scale, scale, scale);
      }
    }
  }
}
