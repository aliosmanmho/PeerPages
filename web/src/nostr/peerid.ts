/**
 * Ziyaretci URL'sindeki peer-id'nin ayristirilmasi.
 *
 * Bicim: `#npub1…` ya da `#npub1…:secret`
 *
 * peer-id TAM npub'dir. README bolum 4 "npub'in kisa formu" diyordu ama
 * NIP-44 sifreleme ve `#p` tag'i tam 32 baytlik pubkey gerektiriyor;
 * kisaltilmis id'den turetilemez. Karar ve gerekcesi README bolum 8'de.
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
}

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
  const npub = colon >= 0 ? raw.slice(0, colon) : raw;
  const secretPart = colon >= 0 ? raw.slice(colon + 1) : "";

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

  return { npub, pubkey, secret: secretPart === "" ? null : secretPart };
}

/** Ekranda gosterim icin npub'i kisaltir: ilk 12 + son 4 (Go tarafiyla ayni). */
export function shortenNpub(npub: string): string {
  if (npub.length <= 20) return npub;
  return `${npub.slice(0, 12)}…${npub.slice(-4)}`;
}
