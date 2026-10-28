/**
 * Letting the visitor override the relay list from the URL.
 *
 * BRIEF section 4: "Default relay list (at least 3, **user
 * overridable**)". This file implements the override:
 *
 *     .../connect/?relay=wss://nos.lol&relay=wss://nostr.mom#npub1...
 *
 * It is also how the Nostr e2e test reaches a local fake relay, so there is
 * no separate back door for tests.
 */

export class RelayListError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RelayListError";
  }
}

/** Validates and normalizes one relay address (same rule as the Go side). */
export function normalizeRelayURL(raw: string): string {
  const s = raw.trim();
  if (s === "") {
    throw new RelayListError("empty relay address");
  }

  let u: URL;
  try {
    u = new URL(s);
  } catch {
    throw new RelayListError(`relay address could not be read: ${s}`);
  }
  if (u.protocol !== "ws:" && u.protocol !== "wss:") {
    throw new RelayListError(`relay address must be ws:// or wss://: ${s}`);
  }
  if (u.hostname === "") {
    throw new RelayListError(`relay address has no host: ${s}`);
  }

  u.hash = "";
  u.search = "";
  // Drop a trailing slash so the same relay cannot be listed twice.
  const text = u.toString();
  return text.endsWith("/") ? text.slice(0, -1) : text;
}

/**
 * Reads the relay list from the URL query string.
 *
 * The `relay` parameter may repeat, and a comma-separated list is accepted.
 * Absent, it returns `null` (the default list is used).
 *
 * @throws {RelayListError} when one of the addresses is invalid — silently
 * falling back to the default would hide that the user's list was ignored.
 */
export function relaysFromSearch(search: string): string[] | null {
  const params = new URLSearchParams(search);
  const raw = params.getAll("relay").flatMap((v) => v.split(","));
  if (raw.length === 0) return null;

  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of raw) {
    if (item.trim() === "") continue;
    const url = normalizeRelayURL(item);
    if (seen.has(url)) continue;
    seen.add(url);
    out.push(url);
  }
  if (out.length === 0) {
    throw new RelayListError("relay parameter given but empty");
  }
  return out;
}
