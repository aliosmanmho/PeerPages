/**
 * The visitor-side Nostr relay layer.
 *
 * Go counterpart: `agent/internal/nostr`. It implements the two rules from
 * BRIEF section 4:
 *  - the same message goes to ALL relays, and succeeds if AT LEAST ONE
 *    accepts it,
 *  - if one relay goes down the others keep working.
 */

import type { EventTemplate, Event as NostrEvent } from "nostr-tools/core";
import {
  getConversationKey,
  decrypt as nip44Decrypt,
  encrypt as nip44Encrypt,
} from "nostr-tools/nip44";
import { SimplePool } from "nostr-tools/pool";
import { finalizeEvent } from "nostr-tools/pure";

/** Signaling event kind: in the ephemeral range (20000-29999), so relays do not store it. */
export const KIND_SIGNALING = 25050;

/** Name of the tag that carries the recipient pubkey. */
export const TAG_RECIPIENT = "p";

/**
 * Default relay list.
 *
 * Verified live with `peerpages doctor` on 2026-10-26: all four connected,
 * accepted an ephemeral kind 25050 event and delivered it back to the
 * subscriber. Must stay the SAME as `DefaultRelays` on the Go side.
 */
export const DEFAULT_RELAYS = [
  "wss://nos.lol",
  "wss://relay.primal.net",
  "wss://nostr.mom",
  "wss://relay.damus.io",
];

export class RelayError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RelayError";
  }
}

export interface PublishReport {
  readonly ok: string[];
  readonly failed: Array<{ relay: string; reason: string }>;
}

export interface SignalingPoolOptions {
  readonly relays?: readonly string[];
  /** Upper bound for a single publish to complete. */
  readonly publishTimeoutMs?: number;
}

const DEFAULT_PUBLISH_TIMEOUT = 15_000;

/**
 * The relay pool.
 *
 * `SimplePool` already handles reconnects and de-duplicating events; on
 * top of it we add the hedged publish and the NIP-44 layer.
 */
export class SignalingPool {
  private readonly pool = new SimplePool();
  private readonly relays: string[];
  private readonly publishTimeoutMs: number;
  private closed = false;

  constructor(opts: SignalingPoolOptions = {}) {
    this.relays = [...(opts.relays ?? DEFAULT_RELAYS)];
    if (this.relays.length === 0) {
      throw new RelayError("at least one relay is required");
    }
    this.publishTimeoutMs = opts.publishTimeoutMs ?? DEFAULT_PUBLISH_TIMEOUT;
  }

  get relayURLs(): readonly string[] {
    return this.relays;
  }

  /**
   * Encrypts the payload to the recipient and sends it to every relay.
   *
   * It counts as a success if AT LEAST ONE relay accepts it (hedged). If
   * none does, it throws RelayError.
   */
  async publish(
    secretKey: Uint8Array,
    recipientPubKey: string,
    payload: string,
  ): Promise<PublishReport> {
    if (this.closed) throw new RelayError("the pool is closed");

    const content = nip44Encrypt(payload, getConversationKey(secretKey, recipientPubKey));

    const template: EventTemplate = {
      kind: KIND_SIGNALING,
      created_at: Math.floor(Date.now() / 1000),
      tags: [[TAG_RECIPIENT, recipientPubKey]],
      content,
    };
    const event = finalizeEvent(template, secretKey);

    const ok: string[] = [];
    const failed: Array<{ relay: string; reason: string }> = [];

    const results = await Promise.allSettled(
      this.pool
        .publish(this.relays, event)
        .map((p, i) => withTimeout(p, this.publishTimeoutMs, this.relays[i] ?? "?")),
    );
    results.forEach((r, i) => {
      const relay = this.relays[i] ?? "?";
      if (r.status === "fulfilled") {
        ok.push(relay);
      } else {
        failed.push({ relay, reason: String(r.reason) });
      }
    });

    if (ok.length === 0) {
      throw new RelayError(
        `no relay accepted the event (${this.relays.length} relays tried): ` +
          failed.map((f) => `${f.relay}: ${f.reason}`).join(" | "),
      );
    }
    return { ok, failed };
  }

  /**
   * Subscribes to the signaling events addressed to us.
   *
   * The returned function unsubscribes. `onPayload` gets the NIP-44
   * decrypted payload and the sender pubkey; events that cannot be
   * decrypted are skipped silently (someone else's, or malformed).
   */
  subscribe(
    secretKey: Uint8Array,
    ownPubKey: string,
    onPayload: (payload: string, fromPubKey: string) => void,
    onRelayCount?: (connected: number) => void,
  ): () => void {
    if (this.closed) throw new RelayError("the pool is closed");

    let connected = 0;
    const sub = this.pool.subscribe(
      this.relays,
      {
        kinds: [KIND_SIGNALING],
        "#p": [ownPubKey],
        // Ephemeral events are not stored; looking at the past is
        // pointless.
        since: Math.floor(Date.now() / 1000) - 60,
      },
      {
        onevent: (event: NostrEvent) => {
          // Ignore our own publish coming back.
          if (event.pubkey === ownPubKey) return;
          let payload: string;
          try {
            payload = nip44Decrypt(event.content, getConversationKey(secretKey, event.pubkey));
          } catch {
            // Not encrypted to us, or malformed: skip.
            return;
          }
          onPayload(payload, event.pubkey);
        },
        oneose: () => {
          connected += 1;
          onRelayCount?.(connected);
        },
      },
    );

    return () => {
      sub.close();
    };
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.pool.close(this.relays);
  }
}

function withTimeout<T>(p: Promise<T>, ms: number, label: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label}: timed out`)), ms);
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
