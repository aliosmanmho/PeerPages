/**
 * Ziyaretci URL'sindeki peer-id'nin ayristirilmasi.
 *
 * Bicim (dilbilgisi): `#` npub [ "/" hedef ] [ ":" sir ]
 *
 * Ornekler: `#npub1…`, `#npub1…/admin`, `#npub1…:gizli`, `#npub1…/admin:gizli`
 *
 * peer-id TAM npub'dir. BRIEF bolum 4 "npub'in kisa formu" diyordu ama
 * NIP-44 sifreleme ve `#p` tag'i tam 32 baytlik pubkey gerektiriyor;
 * kisaltilmis id'den turetilemez. Karar ve gerekcesi BRIEF bolum 8'de.
 * Ekranda kisaltilmis GOSTERIM kullaniliyor (`shortenNpub`).
 *
 * Hash'in sunucuya gitmedigini de not etmek gerek: `#` sonrasi tarayicida
 * kalir, yani paylasilan sir GitHub Pages'e ulasmaz.
 */

import { decode } from "nostr-tools/nip19";

export interface PeerTarget {
  /** Ajanin npub'i (URL'de gorulen hali). */
  readonly npub: string;
  /** Ajanin pubkey'i (32 bayt hex) — NIP-44 ve `#p` tag'i icin. */
  readonly pubkey: string;
  /** Paylasilan sir; yoksa null. */
  readonly secret: string | null;
  /**
   * Ziyaretcinin istedigi hedef adi; URL'de yoksa null (ajan default'a duser).
   *
   * Coklu hedef: bir ajan birden cok yerel projeyi yayinlayabilir ve
   * ziyaretci hangisini istedigini URL'de soyler (protocol/signaling.md).
   */
  readonly target: string | null;
}

/** Hedef adi bicimi — Go ve signaling katmanindakiyle ayni kume. */
const TARGET_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$/;

export class PeerIDError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PeerIDError";
  }
}

/**
 * URL hash'inden peer hedefini cikarir.
 *
 * Girdi `#` ile baslayabilir ya da baslamayabilir. Bos/eksik hash icin
 * `null` doner: bu bir hata degil, "ziyaretci henuz bir ajan secmemis"
 * durumudur (baglanti ekrani elle signaling'e duser).
 *
 * @throws {PeerIDError} hash dolu ama gecersizse
 */
export function parseHash(hash: string): PeerTarget | null {
  let raw = hash.trim();
  if (raw.startsWith("#")) raw = raw.slice(1);
  if (raw === "") return null;

  // Sir, npub'dan sonra ilk ':' ile ayrilir. npub'in kendisi ':' icermez
  // (bech32 alfabesinde yok), bu yuzden ilk ':' guvenli bir ayirac.
  const colon = raw.indexOf(":");
  const head = colon >= 0 ? raw.slice(0, colon) : raw;
  const secretPart = colon >= 0 ? raw.slice(colon + 1) : "";

  // Hedef, npub parcasindan '/' ile ayrilir ve YALNIZCA ':' oncesinde
  // aranir. Sira onemli: sir serbest metindir ve icinde '/' bulunabilir,
  // ama npub'da bulunamaz (bech32 alfabesinde '/' yok). Boylece
  // `#npub…:a/b` sirri "a/b" olarak kalir, `#npub…/admin:a/b` ise hedefi
  // admin, sirri "a/b" olur.
  const slash = head.indexOf("/");
  const npub = slash >= 0 ? head.slice(0, slash) : head;
  const targetPart = slash >= 0 ? head.slice(slash + 1) : "";

  if (!npub.startsWith("npub1")) {
    throw new PeerIDError(
      `peer-id 'npub1' ile baslamali. Adresin '#' sonrasini eksiksiz kopyaladin mi?`,
    );
  }

  let pubkey: string;
  try {
    const decoded = decode(npub);
    if (decoded.type !== "npub" || typeof decoded.data !== "string") {
      throw new PeerIDError(`npub bekleniyordu, gelen: ${decoded.type}`);
    }
    pubkey = decoded.data;
  } catch (err) {
    if (err instanceof PeerIDError) throw err;
    // bech32 checksum'u tutmadiysa burada duser: yanlis kopyalanmis URL.
    throw new PeerIDError("peer-id okunamadi (checksum tutmuyor) — adresi eksiksiz kopyala");
  }

  if (!/^[0-9a-f]{64}$/.test(pubkey)) {
    throw new PeerIDError("peer-id gecersiz bir pubkey tasiyor");
  }

  if (colon >= 0 && secretPart === "") {
    throw new PeerIDError("URL'de ':' var ama sir bos");
  }

  if (slash >= 0 && !TARGET_NAME_RE.test(targetPart)) {
    throw new PeerIDError(
      targetPart === ""
        ? "URL'de '/' var ama hedef adi bos"
        : `hedef adi gecersiz: ${JSON.stringify(targetPart)} (harf/rakam/_/- , en fazla 32)`,
    );
  }

  return {
    npub,
    pubkey,
    secret: secretPart === "" ? null : secretPart,
    target: slash >= 0 ? targetPart : null,
  };
}

/** Ekranda gosterim icin npub'i kisaltir: ilk 12 + son 4 (Go tarafiyla ayni). */
export function shortenNpub(npub: string): string {
  if (npub.length <= 20) return npub;
  return `${npub.slice(0, 12)}…${npub.slice(-4)}`;
}
