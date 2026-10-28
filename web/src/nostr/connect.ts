/**
 * The visitor-side Nostr connection flow.
 *
 * The flow (protocol/signaling.md): the visitor publishes an offer, the
 * agent answers, and the two sides trickle candidates to each other. Go
 * counterpart: `agent/internal/connect`.
 *
 * The visitor generates a NEW ephemeral keypair on every visit: nobody but
 * the agent can link two visitors together.
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

/** Upper bound while waiting for the agent's answer (BRIEF phase 2: 30 s). */
export const ANSWER_TIMEOUT_MS = 30_000;

/** Upper bound for the channels to open. */
export const CHANNELS_TIMEOUT_MS = 60_000;

export type ConnectStage = "relay" | "offer" | "answer" | "ice" | "open";

export interface ConnectHooks {
  /** Reports which step the flow is on (for the status screen). */
  onStage?: (stage: ConnectStage, detail?: string) => void;
}

export interface ConnectResult {
  readonly pc: RTCPeerConnection;
  readonly ctrl: RTCDataChannel;
  readonly data: RTCDataChannel;
  /** Session id; used to send `bye`. */
  readonly sid: string;
  /** Called when closing the tunnel: sends `bye` and drops subscriptions. */
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
  /** Pool that can be injected for tests. */
  readonly pool?: SignalingPool;
  /**
   * ICE servers; the default STUN list (src/ice.ts) when omitted.
   *
   * Until phase 5 this field had no caller and the page passed NO ICE
   * server at all, so it only gathered host candidates.
   */
  readonly iceServers?: RTCIceServer[];
  /**
   * Candidate policy. relay-only is MEANINGLESS on the visitor side (TURN
   * credentials never reach the page), so this is for tests only.
   */
  readonly iceTransportPolicy?: RTCIceTransportPolicy;
}

/**
 * Connects to the agent and returns the open data channels.
 *
 * THIS side opens the channels (role swap, BRIEF section 8): `ctrl` and
 * `data`, both ordered + reliable.
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
      `The shared secret could not be used: ${err instanceof Error ? err.message : String(err)}`,
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

  // Cleanup: on failure too, the subscription and connection must be
  // released.
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
      // Only accept messages from the target agent.
      if (from !== target.pubkey) return;

      let msg: SignalingMsg;
      try {
        msg = codec.decode(payload);
      } catch (err) {
        if (err instanceof SignalingError && err.code === "bad-secret") {
          answerArrived.reject(
            new ConnectError(
              "The shared secret does not match: the agent expects a secret, or the secret is wrong.",
            ),
          );
          return;
        }
        return; // malformed message: skip
      }
      if (msg.sid !== sid) return;

      switch (msg.t) {
        case "answer":
          answerArrived.resolve(msg.sdp);
          break;
        case "candidate": {
          void pc.addIceCandidate({ candidate: msg.cand, sdpMid: msg.mid }).catch(() => {
            // A late or invalid candidate does not drop the connection.
          });
          break;
        }
        case "bye":
          answerArrived.reject(new ConnectError(byeMessage(msg.reason, target.target)));
          break;
        case "offer":
          // The agent never sends an offer.
          break;
      }
    });

    // Send local candidates to the agent (trickle). We do NOT signal the
    // end of candidates: we simply stop sending (protocol/signaling.md).
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
        // Failing to publish a single candidate is not fatal.
      });
    };

    stage("offer");
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    const localSDP = pc.localDescription?.sdp;
    if (!localSDP) throw new ConnectError("the local SDP could not be created");

    // A target name, if there is one, rides in the offer; otherwise the
    // field is not sent at all and the agent falls back to its default
    // (protocol/signaling.md).
    await send(
      target.target
        ? { t: "offer", sid, sdp: localSDP, target: target.target }
        : { t: "offer", sid, sdp: localSDP },
    );

    stage("answer");
    const answerSDP = await withDeadline(
      answerArrived.promise,
      ANSWER_TIMEOUT_MS,
      new ConnectError(
        "The agent did not answer. Is the agent running? (or the relays are unreachable)",
      ),
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
        // bye is best effort: if it cannot be published we close anyway.
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

/** Turns the agent's `bye` reason into something a user can act on. */
function byeMessage(reason: string, targetName: string | null): string {
  switch (reason) {
    case "unknown-target":
      // We do not fall back to the default silently: the visitor may not
      // notice they are browsing the wrong project.
      return targetName
        ? `This agent has no '${targetName}' target. Check the '/${targetName}' part of the URL ` +
            "or ask whoever published the agent for the right address."
        : "The agent did not recognise the requested target.";
    case "too-many-sessions":
      return "The agent is busy with other visitors right now (session limit reached).";
    case "setup-failed":
      return "The agent could not set up the connection.";
    default:
      return `The agent closed the connection: ${reason}`;
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
          "The data channels did not open. The network may be blocking WebRTC " +
            "(with symmetric NAT you need TURN).",
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

// parseSignaling is not used directly here, but the codec's plain path
// calls it; re-exporting keeps tests importing from one place.
export { parseSignaling };
