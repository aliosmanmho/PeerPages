/**
 * Parsing the peer-id out of the visitor URL.
 *
 * Grammar: `#` npub [ "/" target ] [ ":" secret ]
 *
 * Examples: `#npub1...`, `#npub1.../admin`, `#npub1...:secret`
 *
 * The peer-id is the FULL npub. BRIEF section 4 said "the short form of
 * the npub", but NIP-44 encryption and the `#p` tag need the whole
 * 32-byte pubkey, which cannot be derived from a shortened id. Decision
 * and reasoning in BRIEF section 8. A shortened form is used for DISPLAY
 * only (`shortenNpub`).
 *
 * Worth noting: the hash never reaches the server. Everything after `#`
 * stays in the browser, so a shared secret does not reach GitHub Pages.
 */

import { decode } from "nostr-tools/nip19";

export interface PeerTarget {
  /** The agent npub, as seen in the URL. */
  readonly npub: string;
  /** The agent pubkey (32-byte hex) — for NIP-44 and the `#p` tag. */
  readonly pubkey: string;
  /** Shared secret, or null. */
  readonly secret: string | null;
  /**
   * Target the visitor asked for; null means the agent falls back to
   * default.
   *
   * Multi-target: one agent can publish several local projects and the
   * visitor names the one it wants in the URL (protocol/signaling.md).
   */
  readonly target: string | null;
}

/** Target name format — the same set as Go and the signaling layer. */
const TARGET_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$/;

export class PeerIDError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PeerIDError";
  }
}

/**
 * Extracts the peer target from the URL hash.
 *
 * The input may or may not start with `#`. An empty or missing hash
 * returns `null`: that is not an error but "the visitor has not picked an
 * agent yet" (the connect screen falls back to manual signaling).
 *
 * @throws {PeerIDError} when the hash is non-empty but invalid
 */
export function parseHash(hash: string): PeerTarget | null {
  let raw = hash.trim();
  if (raw.startsWith("#")) raw = raw.slice(1);
  if (raw === "") return null;

  // The secret is separated by the first ':' after the npub. An npub
  // never contains ':' (not in the bech32 alphabet), so the first one is
  // a safe separator.
  const colon = raw.indexOf(":");
  const head = colon >= 0 ? raw.slice(0, colon) : raw;
  const secretPart = colon >= 0 ? raw.slice(colon + 1) : "";

  // The target is separated from the npub part by '/', and is only looked
  // for BEFORE the ':'. The order matters: a secret is free text and may
  // contain '/', while an npub cannot (no '/' in bech32). So
  // `#npub...:a/b` keeps "a/b" as the secret, while `#npub.../admin:a/b`
  // means target admin and secret "a/b".
  const slash = head.indexOf("/");
  const npub = slash >= 0 ? head.slice(0, slash) : head;
  const targetPart = slash >= 0 ? head.slice(slash + 1) : "";

  if (!npub.startsWith("npub1")) {
    throw new PeerIDError(`peer-id must start with 'npub1'. Did you copy everything after '#'?`);
  }

  let pubkey: string;
  try {
    const decoded = decode(npub);
    if (decoded.type !== "npub" || typeof decoded.data !== "string") {
      throw new PeerIDError(`expected an npub, got: ${decoded.type}`);
    }
    pubkey = decoded.data;
  } catch (err) {
    if (err instanceof PeerIDError) throw err;
    // A bech32 checksum mismatch lands here: a badly copied URL.
    throw new PeerIDError("peer-id could not be read (checksum mismatch) — copy the whole address");
  }

  if (!/^[0-9a-f]{64}$/.test(pubkey)) {
    throw new PeerIDError("peer-id carries an invalid pubkey");
  }

  if (colon >= 0 && secretPart === "") {
    throw new PeerIDError("the URL has a ':' but the secret is empty");
  }

  if (slash >= 0 && !TARGET_NAME_RE.test(targetPart)) {
    throw new PeerIDError(
      targetPart === ""
        ? "the URL has a '/' but the target name is empty"
        : `invalid target name: ${JSON.stringify(targetPart)} (letters/digits/_/- , 32 max)`,
    );
  }

  return {
    npub,
    pubkey,
    secret: secretPart === "" ? null : secretPart,
    target: slash >= 0 ? targetPart : null,
  };
}

/** Shortens an npub for display: first 12 + last 4 (same as Go). */
export function shortenNpub(npub: string): string {
  if (npub.length <= 20) return npub;
  return `${npub.slice(0, 12)}…${npub.slice(-4)}`;
}
