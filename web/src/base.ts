import { WS_SHIM_FILE } from "./bridge";

/**
 * Base path the app shell is published under (may be a GitHub Pages
 * subpath).
 *
 * Vite hands the build-time `base` value over as
 * `import.meta.env.BASE_URL` ("/" or "/PeerPages/"). The Service Worker
 * scope and every app shell URL derive from it, which is what keeps the SW
 * to its own scope (BRIEF security principle 5).
 */
export const BASE_PATH: string = normalizeBase(import.meta.env.BASE_URL);

function normalizeBase(raw: string | undefined): string {
  if (!raw || raw === "./") return "/";
  let base = raw;
  if (!base.startsWith("/")) base = `/${base}`;
  if (!base.endsWith("/")) base = `${base}/`;
  return base;
}

/** Absolute path under the scope root: `inBase("sw.js")` -> "/PeerPages/sw.js". */
export function inBase(path: string): string {
  return BASE_PATH + path.replace(/^\/+/, "");
}

/**
 * Path of the connect screen, i.e. the tunnel-owner page.
 *
 * NOT at the scope root: that root belongs to the local site. This way
 *  - the visitor sees the site root at its natural URL, and
 *  - the tunnel-owner tab URL never goes through the tunnel, so refreshing
 *    that tab does not close the tunnel.
 * Decision and reasoning: BRIEF section 8.
 */
export const CONNECT_PATH: string = `${BASE_PATH}connect/`;

/**
 * Is this URL one of the app shell's own files?
 *
 * These are NEVER sent through the tunnel; otherwise we would need a
 * tunnel to fetch the connect screen (chicken and egg).
 *
 * The scope root ITSELF is not app shell: that is the local site root and
 * it does go through the tunnel. With no tunnel the SW redirects
 * navigations to the connect screen and 503s subresources (src/sw.ts).
 */
export function isAppShellPath(pathname: string, base: string = BASE_PATH): boolean {
  if (!pathname.startsWith(base)) return false;
  const rest = pathname.slice(base.length);
  return (
    rest === "connect" ||
    rest === "connect/" ||
    rest === "connect/index.html" ||
    rest === "sw.js" ||
    rest === "favicon.ico" ||
    // WebSocket shim (phase 6): an app shell file. It must NOT go through
    // the tunnel, or the visitor's site gets asked for a file it does not
    // have and answers 404.
    rest === WS_SHIM_FILE ||
    rest.startsWith("assets/")
  );
}
