import "./style.css";
import { BASE_PATH, inBase } from "./base";
import { announceTunnelGone, announceTunnelReady, installBridge } from "./page-bridge";
import { decodeSDP, encodeSDP } from "./signal";
import { Tunnel } from "./tunnel";
import { StatusView } from "./ui/status";

/**
 * Baglanti ekrani ve tunel sahibi sayfa.
 *
 * Faz 1: signaling ELLE yapilir. Ajanin bastigi offer buraya yapistirilir,
 * uretilen answer ajana geri yapistirilir. Signaling sunucusu ve Nostr yok
 * (README Faz 1).
 *
 * WebRTC baglantisini BU sayfa tutar; Service Worker icinde WebRTC yok.
 * Sayfa kapanirsa tunel kapanir.
 */

const status = new StatusView();

let tunnel: Tunnel | null = null;

// Olcum ve durum ekrani icin sayaclar.
const counters = { requests: 0, errors: 0, bytes: 0 };

const el = {
  offerInput: document.getElementById("offer-input") as HTMLTextAreaElement | null,
  acceptBtn: document.getElementById("btn-accept-offer") as HTMLButtonElement | null,
  answerOutput: document.getElementById("answer-output") as HTMLTextAreaElement | null,
  copyBtn: document.getElementById("btn-copy-answer") as HTMLButtonElement | null,
  panelManual: document.getElementById("panel-manual"),
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

/** Yapistirilan offer'i kabul eder, answer uretir ve tuneli kurar. */
async function acceptOffer(pasted: string): Promise<void> {
  status.step("signal", "active");
  status.say("Offer okunuyor…");

  let offer: RTCSessionDescriptionInit;
  try {
    offer = decodeSDP(pasted);
  } catch (err) {
    status.step("signal", "error");
    status.say(`Offer okunamadi: ${err instanceof Error ? err.message : String(err)}`, "error");
    return;
  }
  if (offer.type !== "offer") {
    status.step("signal", "error");
    status.say(`Offer bekleniyordu, gelen: ${String(offer.type)}`, "error");
    return;
  }

  const pc = new RTCPeerConnection();

  let ctrl: RTCDataChannel | null = null;
  let data: RTCDataChannel | null = null;

  const bothOpen = new Promise<void>((resolve, reject) => {
    const check = () => {
      if (ctrl && data && ctrl.readyState === "open" && data.readyState === "open") {
        resolve();
      }
    };
    // Kanallari ajan acar; biz yalnizca aliriz.
    pc.ondatachannel = (ev) => {
      const ch = ev.channel;
      if (ch.label === "ctrl") ctrl = ch;
      else if (ch.label === "data") data = ch;
      else {
        console.warn(`[peerpages] beklenmeyen kanal: ${ch.label}`);
        return;
      }
      ch.addEventListener("open", check);
      if (ch.readyState === "open") check();
    };

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
    await pc.setRemoteDescription(offer);
    status.step("signal", "done");
    status.step("ice", "active");
    status.say("ICE adaylari toplaniyor…");

    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    await waitForIceGathering(pc);

    const local = pc.localDescription;
    if (!local) throw new Error("yerel SDP olusturulamadi");

    const encoded = encodeSDP({ type: local.type, sdp: local.sdp });
    if (el.answerOutput) {
      el.answerOutput.value = encoded;
      el.answerOutput.select();
    }
    if (el.copyBtn) el.copyBtn.disabled = false;
    status.say("Answer hazir — ajana yapistir. Kanallar bekleniyor…");

    status.step("tunnel", "active");
    await bothOpen;

    if (!ctrl || !data) throw new Error("kanallar alinamadi");
    tunnel = new Tunnel(ctrl, data);
    announceTunnelReady();

    status.step("tunnel", "done");
    status.say("Tunel acik. Bu sekmeyi kapatma.", "ok");
    showConnected();
  } catch (err) {
    status.step("tunnel", "error");
    status.say(`Tunel kurulamadi: ${err instanceof Error ? err.message : String(err)}`, "error");
    pc.close();
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
  status.step("tunnel", "error");
  status.say(`Tunel kapandi (${state}). Yeni bir offer yapistirabilirsin.`, "error");
  if (el.panelConnected) el.panelConnected.hidden = true;
  if (el.panelManual) el.panelManual.hidden = false;
}

function showConnected(): void {
  if (el.panelManual) el.panelManual.hidden = true;
  if (el.panelConnected) el.panelConnected.hidden = false;
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
  el.acceptBtn?.addEventListener("click", () => {
    const pasted = el.offerInput?.value ?? "";
    if (pasted.trim() === "") {
      status.say("Once ajanin bastigi offer'i yapistir.", "error");
      return;
    }
    if (el.acceptBtn) el.acceptBtn.disabled = true;
    void acceptOffer(pasted).finally(() => {
      if (el.acceptBtn) el.acceptBtn.disabled = false;
    });
  });

  el.copyBtn?.addEventListener("click", async () => {
    const value = el.answerOutput?.value ?? "";
    if (value === "") return;
    try {
      await navigator.clipboard.writeText(value);
      status.say("Answer kopyalandi — ajana yapistir.");
    } catch {
      el.answerOutput?.select();
      status.say("Kopyalanamadi; metni elle sec ve kopyala.", "error");
    }
  });

  // Sekme kapanirken SW'ye haber ver: bir sonraki istek bosa beklemesin.
  window.addEventListener("pagehide", () => {
    if (tunnel) announceTunnelGone();
  });
}

async function main(): Promise<void> {
  wireUI();
  const reg = await registerServiceWorker();
  if (!reg) return;
  status.step("signal", "active");
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
      acceptOffer: (pasted: string) => Promise<void>;
      answer: () => string;
    };
  }
}

window.__peerpages = {
  hasTunnel: () => tunnel?.isOpen === true,
  stats: () => ({ ...counters, tunnel: tunnel?.stats ?? null }),
  ping: async () => (tunnel ? tunnel.ping() : Promise.reject(new Error("tunel yok"))),
  acceptOffer: (pasted: string) => acceptOffer(pasted),
  answer: () => el.answerOutput?.value ?? "",
};
