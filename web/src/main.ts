import "./style.css";
import { BASE_PATH, inBase } from "./base";
import { type ConnectResult, connectToAgent } from "./nostr/connect";
import { PeerIDError, type PeerTarget, parseHash, shortenNpub } from "./nostr/peerid";
import { RelayListError, relaysFromSearch } from "./nostr/relays";
import { announceTunnelGone, announceTunnelReady, installBridge } from "./page-bridge";
import { decodeSDP, encodeSDP } from "./signal";
import { Tunnel } from "./tunnel";
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
 * README bolum 8'de.
 *
 * WebRTC baglantisini BU sayfa tutar; Service Worker icinde WebRTC yok.
 * Sayfa kapanirsa tunel kapanir.
 */

const status = new StatusView();

let tunnel: Tunnel | null = null;
let peer: RTCPeerConnection | null = null;
let nostrSession: ConnectResult | null = null;

// Olcum ve durum ekrani icin sayaclar.
const counters = { requests: 0, errors: 0, bytes: 0 };

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

function onTunnelLost(state: string): void {
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
    nostrSession.close(`connection-${state}`);
    nostrSession = null;
  }
  status.step("tunnel", "error");
  status.say(`Tunel kapandi (${state}). Yeniden offer uretebilirsin.`, "error");
  if (el.panelConnected) el.panelConnected.hidden = true;
  if (el.panelManual) el.panelManual.hidden = false;
  if (el.makeOfferBtn) el.makeOfferBtn.disabled = false;
}

function showConnected(): void {
  if (el.panelManual) el.panelManual.hidden = true;
  if (el.panelConnected) el.panelConnected.hidden = false;
  // Yerel sitenin koku scope kokunde; bu sayfa connect/ altinda duruyor.
  if (el.linkSite) el.linkSite.href = BASE_PATH;
  renderMetrics();
  void measurePing();
}

async function measurePing(): Promise<void> {
  if (!tunnel) return;
  try {
    const rtt = await tunnel.ping();
    renderMetrics(rtt);
  } catch {
    renderMetrics();
  }
}

function renderMetrics(rttMs?: number): void {
  if (!el.metrics) return;
  const stats = tunnel?.stats;
  const rows: Array<[string, string]> = [
    ["Istek", String(counters.requests)],
    ["Hata", String(counters.errors)],
    ["Alinan", formatBytes(counters.bytes)],
    ["Hedefler", tunnel?.hello?.targets.join(", ") ?? "—"],
    ["Devam eden", String(stats?.inflight ?? 0)],
  ];
  if (rttMs !== undefined) {
    rows.splice(3, 0, ["Gecikme", `${rttMs.toFixed(1)} ms`]);
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

  el.retryBtn?.addEventListener("click", () => {
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
  } catch (err) {
    status.step("signal", "error");
    status.say(err instanceof RelayListError ? err.message : String(err), "error");
    return;
  }

  if (el.panelManual) el.panelManual.hidden = true;
  if (el.panelNostr) el.panelNostr.hidden = false;
  if (el.nostrPeer) el.nostrPeer.textContent = shortenNpub(target.npub);
  if (el.retryBtn) el.retryBtn.disabled = true;

  status.step("signal", "active");
  status.say("Relay'lere baglaniliyor…");

  try {
    const session = await connectToAgent({
      target,
      // README bolum 4: relay listesi kullanici tarafindan degistirilebilir
      // (?relay=wss://...). Verilmezse varsayilan liste.
      ...(relayOverride ? { relays: relayOverride } : {}),
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

    tunnel = new Tunnel(session.ctrl, session.data);
    announceTunnelReady();
    status.step("tunnel", "done");
    status.say("Tunel acik. Bu sekmeyi kapatma.", "ok");
    showConnected();
  } catch (err) {
    status.step("tunnel", "error");
    status.say(err instanceof Error ? err.message : String(err), "error");
    if (el.retryBtn) el.retryBtn.disabled = false;
  }
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
    };
  }
}

window.__peerpages = {
  hasTunnel: () => tunnel?.isOpen === true,
  stats: () => ({ ...counters, tunnel: tunnel?.stats ?? null }),
  ping: async () => (tunnel ? tunnel.ping() : Promise.reject(new Error("tunel yok"))),
  makeOffer: () => makeOffer(),
  offer: () => el.offerOutput?.value ?? "",
  acceptAnswer: (pasted: string) => acceptAnswer(pasted),
};
