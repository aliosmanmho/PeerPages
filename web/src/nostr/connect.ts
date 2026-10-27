/**
 * Ziyaretci tarafindaki Nostr baglanti akisi.
 *
 * Akis (protocol/signaling.md): ziyaretci offer yayinlar, ajan answer doner,
 * iki taraf trickle candidate alisverisi yapar. Go karsiligi:
 * `agent/internal/connect`.
 *
 * Ziyaretci her ziyarette YENI gecici keypair uretir: ajan disinda kimse
 * ziyaretcileri birbirine baglayamaz.
 */

import { generateSecretKey, getPublicKey } from "nostr-tools/pure";

import { iceServers } from "../ice";

import type { PeerTarget } from "./peerid";
import { SignalingPool } from "./pool";
import {
  type Codec,
  codecFor,
  newSID,
  parseSignaling,
  SignalingError,
  type SignalingMsg,
} from "./signaling";

/** Ajandan answer beklerken ust sinir (README Faz 2: 30 sn). */
export const ANSWER_TIMEOUT_MS = 30_000;

/** Kanallarin acilmasi icin ust sinir. */
export const CHANNELS_TIMEOUT_MS = 60_000;

export type ConnectStage = "relay" | "offer" | "answer" | "ice" | "open";

export interface ConnectHooks {
  /** Akisin hangi adimda oldugunu bildirir (durum ekrani icin). */
  onStage?: (stage: ConnectStage, detail?: string) => void;
}

export interface ConnectResult {
  readonly pc: RTCPeerConnection;
  readonly ctrl: RTCDataChannel;
  readonly data: RTCDataChannel;
  /** Oturum kimligi; `bye` yollamak icin. */
  readonly sid: string;
  /** Tuneli kapatirken cagrilir: `bye` yollar ve abonelikleri birakir. */
  readonly close: (reason: string) => void;
}

export class ConnectError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConnectError";
  }
}

export interface ConnectOptions {
  readonly target: PeerTarget;
  readonly relays?: readonly string[];
  readonly hooks?: ConnectHooks;
  /** Test icin enjekte edilebilir havuz. */
  readonly pool?: SignalingPool;
  /**
   * ICE sunuculari; verilmezse varsayilan STUN listesi (src/ice.ts).
   *
   * Faz 5'e kadar bu alanin tek cagirani yoktu ve sayfa HIC ICE sunucusu
   * vermiyordu, yani yalnizca host adayi topluyordu.
   */
  readonly iceServers?: RTCIceServer[];
  /**
   * Aday politikasi. Ziyaretci tarafinda relay-only ANLAMSIZ (TURN kimlik
   * bilgisi sayfaya gelmiyor), bu yuzden yalnizca testler icin.
   */
  readonly iceTransportPolicy?: RTCIceTransportPolicy;
}

/**
 * Ajana baglanir ve acik data channel'lari dondurur.
 *
 * Kanallari BU TARAF acar (rol degisimi, README bolum 8): `ctrl` ve `data`,
 * ikisi de ordered + reliable.
 */
export async function connectToAgent(opts: ConnectOptions): Promise<ConnectResult> {
  const { target, hooks } = opts;
  const stage = (s: ConnectStage, detail?: string) => hooks?.onStage?.(s, detail);

  const secretKey = generateSecretKey();
  const pubkey = getPublicKey(secretKey);
  const sid = newSID();

  let codec: Codec;
  try {
    codec = codecFor(target.secret);
  } catch (err) {
    throw new ConnectError(
      `Paylasilan sir kullanilamadi: ${err instanceof Error ? err.message : String(err)}`,
    );
  }

  const ownPool = opts.pool === undefined;
  const pool = opts.pool ?? new SignalingPool({ relays: opts.relays });

  stage("relay", `${pool.relayURLs.length} relay`);

  const pc = new RTCPeerConnection({
    iceServers: opts.iceServers ?? iceServers(null),
    ...(opts.iceTransportPolicy ? { iceTransportPolicy: opts.iceTransportPolicy } : {}),
  });
  const ctrl = pc.createDataChannel("ctrl", { ordered: true });
  const data = pc.createDataChannel("data", { ordered: true });

  // Temizlik: basarisizlikta da abonelik ve baglanti birakilmali.
  let unsubscribe: (() => void) | null = null;
  const cleanup = () => {
    unsubscribe?.();
    unsubscribe = null;
    if (ownPool) pool.close();
  };

  const send = async (msg: SignalingMsg): Promise<void> => {
    await pool.publish(secretKey, target.pubkey, codec.encode(msg));
  };

  const channelsOpen = waitForChannels(ctrl, data, CHANNELS_TIMEOUT_MS);
  const answerArrived = createDeferred<string>();

  try {
    unsubscribe = pool.subscribe(secretKey, pubkey, (payload, from) => {
      // Yalnizca hedef ajandan gelen mesajlari kabul et.
      if (from !== target.pubkey) return;

      let msg: SignalingMsg;
      try {
        msg = codec.decode(payload);
      } catch (err) {
        if (err instanceof SignalingError && err.code === "bad-secret") {
          answerArrived.reject(
            new ConnectError("Paylasilan sir uyusmuyor: ajan sir bekliyor ya da sir yanlis."),
          );
          return;
        }
        return; // bozuk mesaj: atla
      }
      if (msg.sid !== sid) return;

      switch (msg.t) {
        case "answer":
          answerArrived.resolve(msg.sdp);
          break;
        case "candidate": {
          void pc.addIceCandidate({ candidate: msg.cand, sdpMid: msg.mid }).catch(() => {
            // Gec gelen ya da gecersiz aday: baglantiyi dusurmez.
          });
          break;
        }
        case "bye":
          answerArrived.reject(new ConnectError(byeMessage(msg.reason, target.target)));
          break;
        case "offer":
          // Ajan offer yollamaz.
          break;
      }
    });

    // Yerel adaylari ajana yolla (trickle). Aday sonunu BILDIRMIYORUZ:
    // sadece gondermeyi birakiyoruz (protocol/signaling.md).
    pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      const cand = ev.candidate.candidate;
      if (cand === "") return;
      void send({
        t: "candidate",
        sid,
        cand,
        mid: ev.candidate.sdpMid ?? "0",
      }).catch(() => {
        // Tek bir adayin yayinlanamamasi olumcul degil.
      });
    };

    stage("offer");
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    const localSDP = pc.localDescription?.sdp;
    if (!localSDP) throw new ConnectError("yerel SDP olusturulamadi");

    // Hedef adi varsa offer'da tasinir; yoksa alan hic gonderilmez ve ajan
    // default'a duser (protocol/signaling.md).
    await send(
      target.target
        ? { t: "offer", sid, sdp: localSDP, target: target.target }
        : { t: "offer", sid, sdp: localSDP },
    );

    stage("answer");
    const answerSDP = await withDeadline(
      answerArrived.promise,
      ANSWER_TIMEOUT_MS,
      new ConnectError("Ajan yanit vermedi. Ajan calisiyor mu? (ya da relay'lere ulasilamiyor)"),
    );
    await pc.setRemoteDescription({ type: "answer", sdp: answerSDP });

    stage("ice");
    await channelsOpen;
    stage("open");

    return {
      pc,
      ctrl,
      data,
      sid,
      close: (reason: string) => {
        // bye "en iyi gayret": yayinlanamazsa da kapatmaya devam ederiz.
        void send({ t: "bye", sid, reason: reason.slice(0, 64) }).catch(() => {});
        cleanup();
        pc.close();
      },
    };
  } catch (err) {
    cleanup();
    pc.close();
    throw err;
  }
}

/** Ajandan gelen `bye` nedenini kullaniciya anlasilir hale getirir. */
function byeMessage(reason: string, targetName: string | null): string {
  switch (reason) {
    case "unknown-target":
      // Sessizce default'a dusmuyoruz: ziyaretci yanlis projeyi gezdigini
      // fark etmeyebilir.
      return targetName
        ? `Bu ajanda '${targetName}' hedefi yok. URL'deki '/${targetName}' kismini kontrol et ` +
            "ya da ajani yayinlayan kisiden dogru adresi iste."
        : "Ajan istenen hedefi tanimadi.";
    case "too-many-sessions":
      return "Ajan su an baska ziyaretcilerle mesgul (oturum tavani dolu).";
    case "setup-failed":
      return "Ajan baglantiyi kuramadi.";
    default:
      return `Ajan baglantiyi kapatti: ${reason}`;
  }
}

function waitForChannels(
  ctrl: RTCDataChannel,
  data: RTCDataChannel,
  timeoutMs: number,
): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const check = () => {
      if (ctrl.readyState === "open" && data.readyState === "open") {
        clearTimeout(timer);
        resolve();
      }
    };
    const timer = setTimeout(() => {
      reject(
        new ConnectError(
          "Veri kanallari acilmadi. Ag WebRTC'yi engelliyor olabilir " +
            "(simetrik NAT ise TURN gerekir).",
        ),
      );
    }, timeoutMs);

    ctrl.addEventListener("open", check);
    data.addEventListener("open", check);
    check();
  });
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: Error) => void;
}

function createDeferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function withDeadline<T>(p: Promise<T>, ms: number, onTimeout: Error): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(onTimeout), ms);
    p.then(
      (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      (e) => {
        clearTimeout(timer);
        reject(e);
      },
    );
  });
}

// parseSignaling dogrudan kullanilmiyor ama codec'in duz yolu onu cagiriyor;
// re-export ederek testlerin tek yerden import etmesini kolaylastiriyoruz.
export { parseSignaling };
