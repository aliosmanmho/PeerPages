/**
 * ICE configuration on the visitor side.
 *
 * Until phase 5 this page passed NO ICE servers at all
 * (`new RTCPeerConnection(undefined)`), so it only gathered **host
 * candidates**: cross-network connections leaned entirely on the agent
 * learning the visitor peer-reflexively from a packet arriving at its srflx
 * candidate. With a default STUN list the visitor gathers its own srflx
 * candidate too, and the direct path works on more NATs.
 *
 * **TURN credentials NEVER reach this file.** TURN is used on the agent side
 * only: the visitor creates the offer before hearing a single word from the
 * agent, so shipping credentials over signaling would arrive too late.
 * One-sided TURN is enough — the agent gathers a relay candidate and the
 * visitor sends to that public address. Quota and secret stay with the owner
 * (BRIEF section 6, principle 4: the visitor is untrusted). Details in
 * BRIEF section 8.
 */

import { t } from "./i18n";

export class IceConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "IceConfigError";
  }
}

/**
 * Default STUN list.
 *
 * Must be IDENTICAL to `ice.DefaultSTUN` on the Go side.
 */
export const DEFAULT_STUN = [
  "stun:stun.l.google.com:19302",
  "stun:stun1.l.google.com:19302",
] as const;

/** Validates and normalizes a single STUN address. */
export function normalizeStunURL(raw: string): string {
  const s = raw.trim();
  if (s === "") {
    throw new IceConfigError("empty STUN address");
  }
  // STUN URIs are opaque: "stun:host:port" with no `//`, so URL() will not
  // parse the host and we check the scheme by hand.
  const sep = s.indexOf(":");
  if (sep < 0) {
    throw new IceConfigError(`STUN address must start with stun: or stuns: ${s}`);
  }
  const scheme = s.slice(0, sep);
  if (scheme !== "stun" && scheme !== "stuns") {
    throw new IceConfigError(`STUN address must start with stun: or stuns: ${s}`);
  }

  let host = s.slice(sep + 1);
  if (host === "") {
    throw new IceConfigError(`STUN address has no host: ${s}`);
  }
  if (host.includes("?") || host.includes("/")) {
    throw new IceConfigError(`STUN address cannot carry a path or query: ${s}`);
  }
  // Add the default port when none was given (IPv6 arrives in brackets).
  const lastColon = host.lastIndexOf(":");
  const closing = host.lastIndexOf("]");
  if (lastColon <= closing) {
    host = `${host}:${scheme === "stuns" ? 5349 : 3478}`;
  }
  return `${scheme}:${host}`;
}

/**
 * Reads the STUN list from the URL query string.
 *
 * The `stun` parameter may repeat, and a comma-separated list is accepted.
 * Absent, it returns `null` (the default list is used). An empty value
 * (`?stun=`) disables ICE servers entirely: in tests that is the only way to
 * gather host candidates alone.
 *
 * @throws {IceConfigError} when one of the addresses is invalid — silently
 * falling back to the default would hide that the user's list was ignored
 * (the same decision as `relaysFromSearch`).
 */
export function stunFromSearch(search: string): string[] | null {
  const params = new URLSearchParams(search);
  const raw = params.getAll("stun");
  if (raw.length === 0) return null;

  const items = raw.flatMap((v) => v.split(","));
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    if (item.trim() === "") continue;
    const url = normalizeStunURL(item);
    if (seen.has(url)) continue;
    seen.add(url);
    out.push(url);
  }
  // An empty `?stun=` is a deliberate choice: no ICE servers.
  return out;
}

/** Builds an RTCIceServer array from the given list, or the default. */
export function iceServers(stun: readonly string[] | null): RTCIceServer[] {
  const list = stun ?? DEFAULT_STUN;
  if (list.length === 0) return [];
  return [{ urls: [...list] }];
}

/** Which path the connection took. */
export type IceRoute = "host" | "srflx" | "prflx" | "relay" | "unknown";

/** Display text for a route. */
export function routeLabel(route: IceRoute): string {
  switch (route) {
    case "host":
      return t("route.host");
    case "srflx":
      return t("route.srflx");
    case "prflx":
      return t("route.prflx");
    case "relay":
      return t("route.relay");
    default:
      return t("route.unknown");
  }
}

/**
 * Derives the connection path from the selected candidate pair.
 *
 * Why it is needed: until phase 5 the question "is the traffic going
 * through a server" had no answer on screen. With TURN the encryption still
 * holds end to end (the TURN server carries bytes, it cannot see them) but
 * the PATH changes, and the visitor deserves to know.
 *
 * The two sides can report different candidate types; if either says
 * "relay", the traffic is going through the relay.
 */
export function routeFromStats(report: RTCStatsReport): IceRoute {
  let localId: string | undefined;
  let remoteId: string | undefined;
  let found = false;

  report.forEach((entry) => {
    const stat = entry as RTCStats & {
      type: string;
      selected?: boolean;
      state?: string;
      nominated?: boolean;
      localCandidateId?: string;
      remoteCandidateId?: string;
    };
    if (stat.type !== "candidate-pair") return;
    // Chromium uses state=succeeded + nominated instead of `selected`;
    // Firefox provides `selected`. Accept both.
    const chosen =
      stat.selected === true || (stat.state === "succeeded" && stat.nominated === true);
    if (!chosen) return;
    found = true;
    localId = stat.localCandidateId;
    remoteId = stat.remoteCandidateId;
  });

  if (!found) return "unknown";

  const typeOf = (id?: string): IceRoute | null => {
    if (!id) return null;
    const cand = report.get(id) as (RTCStats & { candidateType?: string }) | undefined;
    switch (cand?.candidateType) {
      case "host":
      case "srflx":
      case "prflx":
      case "relay":
        return cand.candidateType;
      default:
        return null;
    }
  };

  const local = typeOf(localId);
  const remote = typeOf(remoteId);

  // If either side is a relay, the traffic goes through the relay.
  if (local === "relay" || remote === "relay") return "relay";
  return local ?? remote ?? "unknown";
}
