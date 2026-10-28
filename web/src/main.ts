import "./style.css";
import { BASE_PATH, inBase } from "./base";
import {
  applyTranslations,
  LANGS,
  type Lang,
  lang,
  resolveLang,
  setLang,
  storedLang,
  t,
} from "./i18n";
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
import { StatusView, type Tone } from "./ui/status";

/**
 * The connect screen, and the page that owns the tunnel.
 *
 * Two paths:
 *  - **Nostr (default):** when the URL carries a peer-id (`#npub1...`) the
 *    connection is established on its own; no copy-paste.
 *  - **Manual (`--manual`):** with no peer-id in the URL this page creates an
 *    offer, the user pastes it into the agent and brings the answer back.
 *
 * Role swap (phase 2): the visitor creates the channels (`ctrl`/`data`) and
 * the offer, the agent answers. Reason: over Nostr the agent cannot know the
 * visitor ephemeral pubkey in advance, so it cannot send the first message.
 * Decision and rationale in BRIEF section 8.
 *
 * THIS page holds the WebRTC connection; there is no WebRTC inside a Service
 * Worker. Close the page and the tunnel closes.
 */

const status = new StatusView();

/**
 * The last status message, kept as a key so a language switch can redraw it.
 *
 * `StatusView.say` takes a finished string on purpose — it is a dumb DOM
 * sink. Remembering the key here is what makes the switcher able to restate
 * the current situation in the new language instead of leaving a stale
 * sentence on screen.
 */
let lastStatus: { key: string; params?: Record<string, string | number>; tone: Tone } | null = null;

function sayKey(key: string, params?: Record<string, string | number>, tone: Tone = "info"): void {
  lastStatus = params ? { key, params, tone } : { key, tone };
  status.say(t(key, params), tone);
}

/** Error message as a string, whatever was thrown. */
function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * Turns a disconnect reason into display text.
 *
 * Our own reasons have translations; WebRTC state names ("failed",
 * "disconnected") pass through as they are, because they are what a reader
 * would search for.
 */
function reasonLabel(reason: string): string {
  return reason === "no-pong" ? t("reason.noPong") : reason;
}

/**
 * Picks the language, applies it to the DOM and wires the picker.
 *
 * Runs before anything else draws, so the first paint is already in the
 * right language.
 */
function initLanguage(): void {
  const navLangs = typeof navigator === "undefined" ? [] : (navigator.languages ?? []);
  setLang(resolveLang(location.search, storedLang(), navLangs));
  applyTranslations(document);

  const select = document.getElementById("lang-select") as HTMLSelectElement | null;
  if (!select) return;
  select.value = lang();
  select.addEventListener("change", () => {
    const next = select.value as Lang;
    if (!LANGS.includes(next)) return;
    setLang(next);
    applyTranslations(document);
    // Redraw the parts that were built in JS rather than marked up in HTML.
    if (lastStatus) status.say(t(lastStatus.key, lastStatus.params), lastStatus.tone);
    if (el.retryBtn) el.retryBtn.textContent = t("btn.retryNow");
    renderMetrics();
  });
}

let tunnel: Tunnel | null = null;
let peer: RTCPeerConnection | null = null;
let nostrSession: ConnectResult | null = null;
/** Target name from the URL; null means the agent falls back to default. */
let activeTarget: string | null = null;

// Counters for the measurements and the status panel.
const counters = { requests: 0, errors: 0, bytes: 0 };

/**
 * Latency is measured CONTINUOUSLY, not once at connect time: a single
 * reading taken at setup says nothing about the tunnel while it is in use.
 */
const LATENCY_INTERVAL_MS = 5000;
const LATENCY_HISTORY = 12;
const latencies: number[] = [];
let latencyTimer: number | null = null;

/** Last sample for the rate calculation; tunnel.stats has no timestamp. */
let lastSample: { at: number; bytesIn: number; bytesOut: number } | null = null;
let rateBps = 0;

/**
 * Reconnect supervisor (phase 4); only on the Nostr path.
 *
 * Automatic reconnection is impossible on the manual path: a human carries
 * the offer.
 */
let supervisor: Supervisor | null = null;
/** Target and relay list to reuse when reconnecting. */
let reconnectTarget: PeerTarget | null = null;
let reconnectRelays: string[] | null = null;
/** STUN list from the URL; null means the default. */
let stunOverride: string[] | null = null;
/** Which path the connection took, i.e. whether TURN is in use. */
let iceRoute: IceRoute = "unknown";
/** Number of open ws sessions (phase 6). */
let openSockets = 0;
/** Set while we tear down, so our own state events are ignored. */
let tearingDown = false;

/**
 * After this many consecutive failed pings the tunnel counts as lost.
 *
 * Why it is needed: the WebRTC state machine does not report every break —
 * channels can read "open" while the far side is gone. A ping already goes
 * out every 5 seconds, so this is a cheap liveness check.
 */
const PING_FAILURES_LOST = 2;
let pingFailures = 0;

const el = {
  offerOutput: document.getElementById("offer-output") as HTMLTextAreaElement | null,
  makeOfferBtn: document.getElementById("btn-make-offer") as HTMLButtonElement | null,
  copyOfferBtn: document.getElementById("btn-copy-offer") as HTMLButtonElement | null,
  answerInput: document.getElementById("answer-input") as HTMLTextAreaElement | null,
  acceptAnswerBtn: document.getElementById("btn-accept-answer") as HTMLButtonElement | null,
  panelIntro: document.getElementById("panel-intro"),
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
    console.warn(`[peerpages] request failed: ${path} - ${message}`);
  },
  onSocketChange: (open) => {
    openSockets = open;
    renderMetrics();
  },
});

async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) {
    status.step("sw", "error");
    sayKey("status.noServiceWorker", undefined, "error");
    return null;
  }

  status.step("sw", "active");
  sayKey("status.swInstalling");

  try {
    // Scope is bounded by BASE_PATH: the SW only catches requests inside
    // its own scope, never other origins or parent paths (principle 5).
    const reg = await navigator.serviceWorker.register(inBase("sw.js"), { scope: BASE_PATH });
    await navigator.serviceWorker.ready;

    // On a first load the page may not be controlled yet, and we cannot
    // message the SW before a controller exists.
    if (!navigator.serviceWorker.controller) {
      await new Promise<void>((resolve) => {
        const onChange = () => {
          navigator.serviceWorker.removeEventListener("controllerchange", onChange);
          resolve();
        };
        navigator.serviceWorker.addEventListener("controllerchange", onChange);
        // Sometimes controllerchange never fires because an active SW is
        // already there: carry on after a short wait.
        setTimeout(resolve, 1500);
      });
    }

    status.step("sw", "done");
    sayKey("status.swActive");
    return reg;
  } catch (err) {
    status.step("sw", "error");
    sayKey("status.swFailed", { err: String(err) }, "error");
    return null;
  }
}

/**
 * Starts a session: opens the channels, creates an offer and shows it.
 *
 * THIS side opens the channels (the role swap): `ctrl` and `data`, both
 * ordered + reliable (protocol/README.md).
 */
async function makeOffer(): Promise<void> {
  status.step("signal", "active");
  sayKey("status.channelsOpening");

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
    setTimeout(() => reject(new Error("channels did not open within 60 s")), 60_000);
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
    if (!local) throw new Error("local SDP was not produced");

    const encoded = encodeSDP({ type: local.type, sdp: local.sdp });
    if (el.offerOutput) {
      el.offerOutput.value = encoded;
      el.offerOutput.select();
    }
    if (el.copyOfferBtn) el.copyOfferBtn.disabled = false;
    if (el.acceptAnswerBtn) el.acceptAnswerBtn.disabled = false;

    status.step("signal", "done");
    sayKey("status.offerReady");

    // Build the tunnel once both channels are open.
    void bothOpen
      .then(() => {
        tunnel = new Tunnel(ctrl, data);
        announceTunnelReady();
        status.step("tunnel", "done");
        sayKey("status.tunnelOpen", undefined, "ok");
        showConnected();
      })
      .catch((err) => {
        status.step("tunnel", "error");
        sayKey("status.tunnelFailed", { err: messageOf(err) }, "error");
      });
  } catch (err) {
    status.step("signal", "error");
    sayKey("status.offerFailed", { err: messageOf(err) }, "error");
    pc.close();
    peer = null;
  }
}

/** Applies the answer the agent printed. */
async function acceptAnswer(pasted: string): Promise<void> {
  const pc = peer;
  if (!pc) {
    sayKey("status.makeOfferFirst", undefined, "error");
    return;
  }

  let answer: RTCSessionDescriptionInit;
  try {
    answer = decodeSDP(pasted);
  } catch (err) {
    sayKey("status.answerUnreadable", { err: messageOf(err) }, "error");
    return;
  }
  if (answer.type !== "answer") {
    sayKey("status.answerExpected", { type: String(answer.type) }, "error");
    return;
  }

  try {
    await pc.setRemoteDescription(answer);
    status.step("tunnel", "active");
    sayKey("status.answerApplied");
  } catch (err) {
    status.step("tunnel", "error");
    sayKey("status.answerUnreadable", { err: messageOf(err) }, "error");
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
    // No trickle here, but some browsers are slow to reach "complete".
    setTimeout(resolve, 10_000);
  });
}

/** Releases the open tunnel, the session and the timers. */
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
 * The tunnel dropped.
 *
 * On the Nostr path the supervisor takes over: a brand-new session is built
 * FROM SCRATCH (the agent is answerer-only, so there is no other way). On
 * the manual path automatic reconnection is impossible, so the old
 * behaviour stays.
 */
function onTunnelLost(state: string): void {
  if (tearingDown) return;
  teardownTunnel(`connection-${state}`);

  if (supervisor) {
    status.step("tunnel", "active");
    // Tell the SW, so requests arriving in this gap wait briefly instead
    // of getting a 503.
    announceTunnelReconnecting();
    supervisor.markLost(state);
    return;
  }

  status.step("tunnel", "error");
  sayKey("status.tunnelClosed", { state }, "error");
  if (el.panelConnected) el.panelConnected.hidden = true;
  if (el.panelManual) el.panelManual.hidden = false;
  if (el.makeOfferBtn) el.makeOfferBtn.disabled = false;
}

/** Writes the supervisor state changes to the screen. */
function renderReconnectState(state: SupervisorState, info: SupervisorInfo): void {
  if (el.retryBtn) {
    el.retryBtn.textContent = t("btn.retryNow");
    el.retryBtn.disabled = state !== "reconnecting";
  }
  if (state !== "reconnecting") {
    renderMetrics();
    return;
  }

  // The panel stays open so the counters and the address remain visible:
  // the visitor should see what is still going on.
  const reason = info.reason ? ` (${reasonLabel(info.reason)})` : "";
  if (info.waitMs > 0) {
    sayKey("status.reconnectIn", { reason, seconds: Math.round(info.waitMs / 1000) }, "error");
  } else {
    sayKey("status.reconnectAttempt", { reason, attempt: info.attempt }, "error");
  }
  renderMetrics();
}

function showConnected(): void {
  if (el.panelIntro) el.panelIntro.hidden = true;
  if (el.panelManual) el.panelManual.hidden = true;
  if (el.panelConnected) el.panelConnected.hidden = false;
  // The local site root lives at the scope root; this page sits under
  // connect/.
  if (el.linkSite) el.linkSite.href = BASE_PATH;
  // The share block only makes sense on the Nostr path: with no peer-id in
  // the URL there is nothing worth carrying to another device.
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
    // One failure is not enough: a pong may simply be late. Several in a
    // row mean the far side is gone, even while the channels still read
    // "open".
    pingFailures += 1;
    if (pingFailures >= PING_FAILURES_LOST) {
      onTunnelLost("no-pong");
      return;
    }
  }
  renderMetrics();
}

/**
 * Reads the selected candidate pair and refreshes the connection path.
 *
 * It can come back empty RIGHT AFTER the tunnel opens (the browser may not
 * have published the pair yet), so we look again on every latency tick.
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
    // If getStats is unsupported or the connection is gone, never mind.
  }
}

function median(values: readonly number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) return sorted[mid] ?? 0;
  return ((sorted[mid - 1] ?? 0) + (sorted[mid] ?? 0)) / 2;
}

/** Instantaneous rate: bytes since the last sample over elapsed time. */
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
      ? t("metrics.measuring")
      : t("metrics.latencyValue", {
          last: (latencies[latencies.length - 1] ?? 0).toFixed(1),
          median: median(latencies).toFixed(1),
          n: latencies.length,
        });

  const errors = counters.errors > 0 ? ` (${t("metrics.errors", { n: counters.errors })})` : "";
  const none = t("metrics.none");

  const rows: Array<[string, string]> = [
    [t("metrics.latency"), latency],
    [t("metrics.requests"), `${counters.requests}${errors}`],
    [t("metrics.received"), formatBytes(stats?.bytesIn ?? 0)],
    [t("metrics.sent"), formatBytes(stats?.bytesOut ?? 0)],
    [t("metrics.rate"), rate > 0 ? `${formatBytes(rate)}/s` : none],
    [t("metrics.inflight"), String(stats?.inflight ?? 0)],
    [t("metrics.targets"), tunnel?.hello?.targets.join(", ") ?? none],
    [t("metrics.activeTarget"), activeTarget ?? "default"],
    // If the traffic goes through a TURN server the visitor should know:
    // the encryption still holds end to end, but the PATH changed.
    [t("metrics.route"), routeLabel(iceRoute)],
  ];

  // Reconnects only show once they happened: a row that always reads zero
  // is noise.
  if (supervisor && supervisor.reconnects > 0) {
    rows.push([t("metrics.reconnects"), String(supervisor.reconnects)]);
  }
  if (supervisor?.currentState === "reconnecting") {
    rows.push([t("metrics.state"), t("metrics.stateReconnecting")]);
  }
  // Same for the WebSocket row: only while a session is open (phase 6).
  if (openSockets > 0) {
    rows.push([t("metrics.websocket"), t("metrics.wsOpen", { n: openSockets })]);
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
      sayKey("status.offerCopied");
    } catch {
      el.offerOutput?.select();
      sayKey("status.copyFailed", undefined, "error");
    }
  });

  el.acceptAnswerBtn?.addEventListener("click", () => {
    const pasted = el.answerInput?.value ?? "";
    if (pasted.trim() === "") {
      sayKey("status.pasteAnswerFirst", undefined, "error");
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
    // If the supervisor is already up, skip the countdown and try now.
    if (supervisor) {
      supervisor.retryNow();
      return;
    }
    const hash = location.hash;
    try {
      const target = parseHash(hash);
      if (target) void connectViaNostr(target);
    } catch {
      sayKey("status.badPeerID", undefined, "error");
    }
  });

  // On tab close, tell the SW and send the agent a `bye`: no leaked
  // resources on its side, no pointless wait on the next request.
  window.addEventListener("pagehide", () => {
    stopLatencyProbe();
    // The page is going away; reconnecting makes no sense.
    supervisor?.stop();
    if (tunnel) announceTunnelGone();
    if (nostrSession) {
      nostrSession.close("visitor-left");
      nostrSession = null;
    }
  });
}

/** Connects over Nostr when the URL carries a peer-id. */
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
  // Show the target name too, so the visitor knows which project they are
  // connected to.
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
    // The agent may simply not be up yet: the supervisor keeps trying, so
    // the visitor does not have to reload by hand.
    supervisor.markLost(msg);
  }
}

/**
 * Opens a single Nostr session.
 *
 * Every call builds FROM SCRATCH: new sid, new ephemeral keypair, new
 * offer. Because the agent is answerer-only and a session is one-shot (this
 * was chosen over ICE restart, BRIEF section 8), reconnecting is the same
 * thing.
 */
async function openNostrSession(): Promise<void> {
  const target = reconnectTarget;
  if (!target) throw new Error("no target");
  const relayOverride = reconnectRelays;

  status.step("signal", "active");
  sayKey("status.relaysConnecting");

  const session = await connectToAgent({
    target,
    // BRIEF section 4: the relay list can be overridden by the user
    // (?relay=wss://...). Without it, the default list.
    ...(relayOverride ? { relays: relayOverride } : {}),
    // The STUN list is overridable too (?stun=...); TURN credentials NEVER
    // reach the page (BRIEF section 8).
    iceServers: iceServers(stunOverride),
    hooks: {
      onStage: (stage, detail) => {
        switch (stage) {
          case "relay":
            sayKey("status.relaysConnectingDetail", { detail: detail ?? "" });
            break;
          case "offer":
            sayKey("status.offerPublishing");
            break;
          case "answer":
            status.step("signal", "done");
            status.step("ice", "active");
            sayKey("status.waitingAgent");
            break;
          case "ice":
            sayKey("status.iceConnecting");
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

  // New tunnel: the latency history belonged to the old session, so it is
  // reset. Request and byte counters are KEPT — from the visitor's point of
  // view the same session continues.
  latencies.length = 0;
  lastSample = null;
  pingFailures = 0;
  iceRoute = "unknown";

  tunnel = new Tunnel(session.ctrl, session.data);
  announceTunnelReady();
  status.step("tunnel", "done");
  sayKey("status.tunnelOpen", undefined, "ok");
  showConnected();
  supervisor?.markConnected();
}

async function main(): Promise<void> {
  initLanguage();
  wireUI();

  // With no peer-id in the URL, reveal the intro block IMMEDIATELY: whoever
  // lands here usually came from the README or a launch link and wants the
  // "what is this" answer first. Before the SW step, because they need that
  // answer even when the SW cannot be installed (old browser, locked-down
  // environment).
  if (location.hash.trim().length <= 1 && el.panelIntro) {
    el.panelIntro.hidden = false;
  }

  const reg = await registerServiceWorker();
  if (!reg) return;

  let target: PeerTarget | null = null;
  try {
    target = parseHash(location.hash);
  } catch (err) {
    status.step("signal", "error");
    status.say(err instanceof PeerIDError ? err.message : String(err), "error");
    // A broken peer-id: show the problem instead of falling back to the
    // manual path, otherwise the user starts copy-pasting without noticing
    // that the URL is wrong.
    if (el.panelManual) el.panelManual.hidden = true;
    return;
  }

  if (target) {
    await connectViaNostr(target);
    return;
  }

  // No peer-id: "what is this" first, the manual signaling flow second.
  status.step("signal", "active");
  sayKey("status.noPeerID");
  if (el.panelNostr) el.panelNostr.hidden = true;
}

void main();

// A small window hook so the e2e tests can read the page state. It has no
// effect on production behaviour.
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
      /** Number of open WebSocket sessions (phase 6). */
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
  ping: async () => (tunnel ? tunnel.ping() : Promise.reject(new Error("no tunnel"))),
  makeOffer: () => makeOffer(),
  offer: () => el.offerOutput?.value ?? "",
  acceptAnswer: (pasted: string) => acceptAnswer(pasted),
};

/**
 * Copies the visitor address to the clipboard.
 *
 * The fallback path is genuinely needed: `navigator.clipboard` requires a
 * SECURE CONTEXT, and the two-device trial is exactly that case — when a
 * phone reaches the laptop over a plain IP address there is no clipboard
 * API (docs/MANUAL-TEST.md).
 */
async function copyVisitorURL(): Promise<void> {
  const url = location.href;
  const done = (ok: boolean) => {
    if (!el.copyUrlBtn) return;
    const label = el.copyUrlBtn.textContent ?? "";
    el.copyUrlBtn.textContent = ok ? t("copy.done") : t("copy.failed");
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
    // Denied, or not a secure context: fall through to the backup path.
  }

  // Backup: execCommand through a throwaway textarea.
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

/** Opens and closes the QR panel; renders the code on first open. */
function toggleQR(): void {
  if (!el.qrBox || !el.qrBtn) return;
  const show = el.qrBox.hidden;
  el.qrBox.hidden = !show;
  el.qrBtn.setAttribute("aria-expanded", String(show));
  if (show) drawQR(location.href);
}

/** Draws the QR code onto the canvas. */
function drawQR(text: string): void {
  const canvas = el.qrCanvas;
  if (!canvas) return;

  let code: QRCode;
  try {
    code = encode(text);
  } catch (err) {
    // Too long an address (a long secret, say) cannot be encoded. The copy
    // button still works, so we just state the reason.
    const hint = el.qrBox?.querySelector(".qr__hint");
    if (hint) {
      hint.textContent = t("qr.failed", { err: messageOf(err) });
    }
    return;
  }

  // Four pixels per module: a size a phone camera reads comfortably.
  const scale = 4;
  const quiet = 4; // the quiet zone the standard asks for, in modules
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
