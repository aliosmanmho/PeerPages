# Security model and threat model

PeerPages lets a visitor's browser reach an HTTP server on your machine without
any proxy in between. That is a useful thing and a dangerous-sounding thing, so
this document says exactly what is protected, what is not, and which trade-offs
were made on purpose.

Nothing here is aspirational: every claim points at the file that implements or
specifies it.

## What the system is

Three moving parts, two of which you do not control:

| Part | Runs where | Trusted? |
|---|---|---|
| App shell + Service Worker | the visitor's browser, served from GitHub Pages | **no** (it is the visitor's machine) |
| Nostr relays | public third-party servers | **no** (they carry encrypted blobs) |
| Agent | your machine | yes (it is you) |

The agent is the only component that can touch your network, so almost all of
the security surface is "what can reach the agent, and what can the agent be
talked into doing".

## Threat model

### 1. Someone watching the Nostr relays

**What they see.** That a public key received an event, when, and how large it
was. Signaling content is encrypted with **NIP-44** (`protocol/signaling.md`
§"Taşıma: Nostr event'i"), so the offer, the answer, the ICE candidates and the target name
are not visible to the relay.

**What they do not see.** Any HTTP traffic. Signaling carries only the WebRTC
handshake; the data itself never touches a relay (except the optional TURN case
below).

**What is not hidden.** Metadata: your agent's public key is a stable
identifier, so a relay operator can tell *that* your agent is online and *when*
it receives connection attempts. `--secret` does not hide this — it is
explicitly documented as protecting authorization, not presence
(`protocol/signaling.md` §"Paylaşılan sır katmanı").

**Storage.** Events use ephemeral kind **25050** (20000–29999), which relays are
specified not to store. Relays can misbehave; `peerpages doctor` therefore tests
each relay by publishing an event and reading it back, because a relay that
accepts an event with "OK" and silently drops it would break signaling without
any visible error (`docs/BRIEF.md` §8).

**If you do not want to use public relays** at all, pass your own:
`peerpages serve --relay wss://my-relay.example`.

### 2. Someone guessing or receiving your peer-id

**Guessing.** The peer-id is a **full 32-byte Nostr public key** (npub), not a
short identifier. An earlier design used the first 12 characters of the npub and
was abandoned, both because NIP-44 needs the full key and because prefix search
on relays is unreliable (`docs/BRIEF.md` §8). Guessing it is not a realistic
attack. Session ids (`sid`) are 64 bits of randomness per session.

**Receiving.** A URL can be forwarded, and anyone holding it can reach the
targets you published while the agent runs. **Treat the URL like a password.**
This is the honest limit of the design: there is no account system, so there is
no identity to check.

**Second factor.** `--secret <word>` adds a layer *under* NIP-44: the payload is
sealed with ChaCha20-Poly1305 using a key derived via HKDF-SHA256 from your
secret, and an offer that cannot be opened is rejected with `bad-secret`
(`protocol/signaling.md` §"Paylaşılan sır katmanı"). Someone who knows your
peer-id but not the secret cannot open a session.

**Rotating.** The peer-id comes from `~/.peerpages/key` (an nsec). Delete that
file and the agent generates a new identity — every previously shared URL stops
working. That is the revocation mechanism.

### 3. A malicious visitor

The visitor is assumed hostile by default (`docs/BRIEF.md` §6, principle 4).
Concretely, on the signaling layer (`agent/internal/connect/guard.go`):

| Guard | Value | Stops |
|---|---|---|
| Clock-skew rejection | `created_at` must be within ±5 min | replaying old events |
| Seen-event set | ids remembered for 10 min (> skew window, enforced by a test) | replay inside the accepted window |
| Rate limit per visitor pubkey | 120 events / minute | flooding (trickle ICE legitimately sends dozens) |
| Concurrent session cap | `--max-sessions` (default 4) | resource exhaustion |
| Unknown `sid` candidates rejected | — | state pollution |
| SDP / candidate size limits | — | memory exhaustion |
| Pubkeys shortened in logs | — | unnecessary identity trail |

And on the tunnel layer:

| Guard | Value | File |
|---|---|---|
| Request body ceiling | `--max-body` (50 MB) | `agent/internal/tunnel/tunnel.go` |
| Concurrent request ceiling | `--max-concurrent` (64) | same |
| Long-lived stream ceiling | `--max-streams` (8), shared by SSE + WebSocket | `agent/internal/tunnel/ws.go` |
| WebSocket message ceiling | `--max-ws-message` (1 MB), both directions | same |
| Strict frame ordering | unexpected `seq` aborts the stream | `protocol/README.md` |
| Path validation | must start with `/`; absolute URLs and `//host` rejected | `agent/internal/protocol/ctrl.go` |

A ceiling that silently drops data is worse than no ceiling: in Faz 3 an
oversized early body frame was dropped silently and left the request hanging
forever, turning a protection into a resource leak. It now aborts the request
with a reason the visitor can see (`docs/BRIEF.md` §8).

**What a visitor can still do.** Within the targets you published, a visitor can
do anything an HTTP client can do — including authenticated actions if your
local app trusts the browser session. PeerPages does not add authorization in
front of your app. If `localhost:3000` is an admin panel with no login, sharing
the URL shares the admin panel.

### 4. Someone trying to reach the rest of your network (SSRF)

This is the attack the design takes most seriously, because the agent sits
inside your network.

- **The agent only ever requests hosts you named on the command line.** The
  target URL is resolved exclusively from the allowlist
  (`agent/internal/target.Resolve`); the request path contributes only a path.
  Deriving a host from the path is impossible by construction, not by filtering
  (`docs/BRIEF.md` §6, principle 1).
- Absolute URLs, protocol-relative paths (`//evil.example`), `..` traversal,
  backslashes, NUL bytes and line breaks in the path are rejected — in two
  layers: protocol validation first, `target.Resolve` as the decisive one.
- The same rule applies to the WebSocket bridge: `ws-open` carries a path, never
  a host (`agent/internal/wsbridge/wsbridge.go`).
- There is an end-to-end test that drives SSRF attempts through a real tunnel
  (`agent/internal/proxy` `TestIntegrationSSRFOverTunnel`, and the browser-side
  case in `web/e2e/tunnel.spec.ts`).
- If you run your own TURN server, deny relaying into private ranges —
  `docs/TURN.md` ships a coturn config with `denied-peer-ip` for
  10/8, 172.16/12 and 192.168/16, plus quotas.

## Transport confidentiality

WebRTC data channels are **DTLS-encrypted end to end**. The two endpoints are
the visitor's browser and your agent; no third party holds a key.

With the optional TURN fallback (`--turn`, off by default) a relay forwards the
encrypted packets. It can see the two addresses and the data volume; it cannot
see the content (`docs/TURN.md`). TURN credentials never leave the agent — the
page is never told that TURN exists, so a visitor cannot spend your quota.

## Accepted trade-offs

These are deliberate, documented decisions, not oversights.

1. **`Origin` is sent as the target's own origin** on WebSocket handshakes.
   Local dev servers (Vite, Socket.IO) check `Origin` and would reject the
   visitor's `github.io` origin, so the tunnel presents itself as a local
   client. Consequence: your local server's origin check cannot distinguish
   tunnelled traffic from a real local client. The bridge is off by default,
   which is why this was considered acceptable (`docs/WEBSOCKET.md`).
2. **The WebSocket shim modifies HTML.** With `--inject-ws-shim`, the agent
   marks HTML responses and the Service Worker injects a `<script>` tag. It is
   opt-in for exactly this reason. The agent's proxy itself stays byte-exact; it
   only adds a header (`protocol/README.md` §"Ayrılmış başlıklar").
3. **TURN is the single exception to "no server in between."** Optional, off by
   default, and the connect screen tells the visitor when traffic is going
   through a relay instead of directly.
4. **`--verbose` disables log masking.** By default URL paths are masked
   (`/api/items` → `/*3/*5`) so a shared terminal or a pasted log does not leak
   what the visitor browsed. `--verbose` turns that off; use it for debugging,
   not in a screen share.
5. **Secrets belong in environment variables.** `PEERPAGES_TURN_PASS` and
   `PEERPAGES_CF_TURN_TOKEN` exist because command-line arguments are visible to
   other users via `ps` and Task Manager.
6. **Idle enforcement lives in the agent, not the Service Worker.** The page's
   "I'm gone" message is best-effort (a crashed tab never sends it), so the
   agent pings every 10 s and closes a session after 30 s of silence. The
   Service-Worker-side check is deliberately lazy because a periodic timer in an
   idle worker is unreliable (`docs/BRIEF.md` §8).
7. **The agent key is stored unencrypted** at `~/.peerpages/key`. It is a
   capability, not a credential for anything else; losing it means your URLs
   stop working, and leaking it means someone else can impersonate your
   peer-id. Back it up, do not commit it (it is in `.gitignore`).

## Out of scope

- **Authentication and authorization.** Whoever has the URL (and the secret, if
  set) connects. There is no user model.
- **Denial of service.** The ceilings above bound memory and session counts, but
  a determined attacker who knows your peer-id can keep the agent busy. Stop the
  agent to stop it.
- **Protecting you from the visitor's browser.** Anything the visitor's page
  does is under their control; the agent treats every byte from them as input,
  never as instruction.
- **Hosting.** This is a demo and sharing tool. GitHub Pages' terms are written
  for static showcases; do not present PeerPages as a commercial proxy
  (`docs/BRIEF.md` §8).
- **Formal review.** The cryptography is NIP-44 (x25519 + ChaCha20-Poly1305 +
  HMAC) and DTLS as implemented by the browser and pion — neither our
  composition nor our implementation has had an external security audit. The
  protocol's own test vectors are generated by an **independent third
  implementation** (Python `cryptography`) precisely so that the Go and
  TypeScript sides cannot share a bug (`CONTRIBUTING.md`).

## Verifying the claims yourself

```bash
# SSRF: the agent refuses to leave the allowlist
cd agent && go test ./internal/target/ ./internal/proxy/ -run "SSRF|Resolve" -v

# Signaling guards: replay, clock skew, rate limit
cd agent && go test ./internal/connect/ -run "SeenSet|FreshEnough|RateLimiter" -v

# Visitor-untrusted ceilings over a real WebRTC connection
cd agent && go test ./internal/tunnel/ -run "TooLarge|MaxConcurrent|StreamBudget" -v

# NIP-44 and the secret layer against third-party vectors
cd agent && go test ./internal/signaling/
cd web && npx vitest run src/nostr

# The whole chain in a browser, including an SSRF attempt
cd web && npx playwright test tunnel.spec.ts
```

## Reporting a vulnerability

Please **do not** open a public issue for a security problem.

- Preferred: GitHub Security Advisories —
  <https://github.com/aliosmanmho/PeerPages/security/advisories/new>
- Alternative: open a normal issue saying only "security report, please contact
  me" with no details, and wait to be contacted.

Expect a first response within **7 days**. This is a side project maintained by
one person; there is no paid bounty, and fixes ship when they are ready. If a
report shows that a claim in this document is wrong, the document gets corrected
first — the honesty of these claims is the point.
