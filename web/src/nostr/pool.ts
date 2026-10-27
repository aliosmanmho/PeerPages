/**
 * Ziyaretci tarafindaki Nostr relay katmani.
 *
 * Go karsiligi: `agent/internal/nostr`. BRIEF bolum 4'un iki kuralini
 * uyguluyor:
 *  - Ayni mesaj TUM relay'lere yollanir, EN AZ BIRI kabul ederse basarili.
 *  - Bir relay dusse digerleri calismaya devam eder.
 */

import type { EventTemplate, Event as NostrEvent } from "nostr-tools/core";
import {
  getConversationKey,
  decrypt as nip44Decrypt,
  encrypt as nip44Encrypt,
} from "nostr-tools/nip44";
import { SimplePool } from "nostr-tools/pool";
import { finalizeEvent } from "nostr-tools/pure";

/** Signaling event turu: ephemeral aralikta (20000-29999), relay'ler saklamaz. */
export const KIND_SIGNALING = 25050;

/** Alici pubkey'i tasiyan tag adi. */
export const TAG_RECIPIENT = "p";

/**
 * Varsayilan relay listesi.
 *
 * `peerpages doctor` ile 2026-10-26'da canli dogrulandi: dordu de baglandi,
 * ephemeral kind 25050 event'i kabul etti ve aboneye geri iletti. Go
 * tarafindaki `DefaultRelays` ile AYNI olmali.
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
  /** Tek bir yayinin tamamlanmasi icin ust sinir. */
  readonly publishTimeoutMs?: number;
}

const DEFAULT_PUBLISH_TIMEOUT = 15_000;

/**
 * Relay havuzu.
 *
 * `SimplePool` zaten yeniden baglanma ve tekrarlanan event ayiklamasini
 * yapiyor; biz ustune hedged yayini ve NIP-44 katmanini koyuyoruz.
 */
export class SignalingPool {
  private readonly pool = new SimplePool();
  private readonly relays: string[];
  private readonly publishTimeoutMs: number;
  private closed = false;

  constructor(opts: SignalingPoolOptions = {}) {
    this.relays = [...(opts.relays ?? DEFAULT_RELAYS)];
    if (this.relays.length === 0) {
      throw new RelayError("en az bir relay gerekli");
    }
    this.publishTimeoutMs = opts.publishTimeoutMs ?? DEFAULT_PUBLISH_TIMEOUT;
  }

  get relayURLs(): readonly string[] {
    return this.relays;
  }

  /**
   * Payload'i aliciya sifreleyip tum relay'lere yollar.
   *
   * EN AZ BIR relay kabul ederse basarili sayilir (hedged). Hicbiri kabul
   * etmezse RelayError atar.
   */
  async publish(
    secretKey: Uint8Array,
    recipientPubKey: string,
    payload: string,
  ): Promise<PublishReport> {
    if (this.closed) throw new RelayError("havuz kapali");

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
        `hicbir relay event'i kabul etmedi (${this.relays.length} relay denendi): ` +
          failed.map((f) => `${f.relay}: ${f.reason}`).join(" | "),
      );
    }
    return { ok, failed };
  }

  /**
   * Bize gelen signaling event'lerine abone olur.
   *
   * Donen fonksiyon abonelikten cikar. `onPayload` NIP-44 cozulmus payload'i
   * ve gonderen pubkey'i alir; cozulemeyen event'ler sessizce atlanir
   * (baskasina ait ya da bozuk).
   */
  subscribe(
    secretKey: Uint8Array,
    ownPubKey: string,
    onPayload: (payload: string, fromPubKey: string) => void,
    onRelayCount?: (connected: number) => void,
  ): () => void {
    if (this.closed) throw new RelayError("havuz kapali");

    let connected = 0;
    const sub = this.pool.subscribe(
      this.relays,
      {
        kinds: [KIND_SIGNALING],
        "#p": [ownPubKey],
        // Ephemeral event'ler saklanmaz; gecmise bakmanin anlami yok.
        since: Math.floor(Date.now() / 1000) - 60,
      },
      {
        onevent: (event: NostrEvent) => {
          // Kendi yayinimiz geri gelirse yok say.
          if (event.pubkey === ownPubKey) return;
          let payload: string;
          try {
            payload = nip44Decrypt(event.content, getConversationKey(secretKey, event.pubkey));
          } catch {
            // Bize sifrelenmemis ya da bozuk: atla.
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
    const timer = setTimeout(() => reject(new Error(`${label}: zaman asimi`)), ms);
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
