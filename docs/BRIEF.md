# PeerPages — the internal brief and roadmap

> **What this file is:** the project's internal brief, its phase plan and
> its decision log (section 8). Through phases 0–6 it was the `README.md`
> at the repo root; in phase 7 the root became the **public English
> README** and this file became `docs/BRIEF.md`.
>
> **The references in the code:** comments in the source such as
> `BRIEF section 8`, `BRIEF principle 1` or `BRIEF section 4` point at this
> file; the section numbers were preserved when it moved. The six security
> principles (section 6) also sit in the public README **under the same
> numbers**.
>
> **The name:** `PeerPages` was settled in phase 7 (see section 5,
> phase 7).

## 1. The idea in one sentence

The visitor opens
`https://<user>.github.io/peerpages/#<peer-id>`; the page installs a
Service Worker, opens a WebRTC data channel to the agent on your machine,
and your site on **localhost:3000** works in the visitor's browser like an
ordinary site under the github.io address. There is **no server in
between**: the static page is on GitHub Pages, signaling goes over public
Nostr relays, the traffic is end-to-end encrypted with DTLS, and the agent
is a single binary.

What sets it apart from the alternatives (worth stressing in the README
and at launch):

| | ngrok / Cloudflare Tunnel | web-p2p-tunnel (2024) | **PeerPages** |
|---|---|---|---|
| A relay server | Yes (a third party) | No | No |
| A signaling server | — | Yes (its own WebSocket server) | **No** (Nostr) |
| Signup / account | Required | Not required | **Not required** |
| Install for the visitor | None | None | None |
| Who can see the traffic | The provider | Nobody | **Nobody** |
| Price | A limited free tier | Free | **Free** |

## 2. The architecture

```
The visitor's browser                             Your machine
┌──────────────────────────────┐                  ┌──────────────────────────┐
│ github.io/peerpages/#abc123  │                  │ peerpages agent (Go)     │
│  ├─ app shell (Vite+TS)      │   WebRTC DC      │  ├─ Nostr signaling      │
│  ├─ Service Worker ──fetch──►│◄════════════════►│  ├─ pion/webrtc          │
│  └─ RTCPeerConnection        │   (DTLS, E2E)    │  └─ reverse proxy ──────►│ localhost:3000
└──────────────┬───────────────┘                  └────────────┬─────────────┘
               │ offer/answer/ICE (NIP-44 encrypted, ephemeral events)      │
               └──────────────► Nostr relays (wss://…) ◄───────────────────┘
                                 (public, free, at least 3)
Optional: a TURN fallback — on the AGENT side only, no credential reaches the visitor (phase 5)
```

### The components

- **`web/`** — the static app. Vite + TypeScript, no framework (so the
  bundle stays small). Two pieces:
  - `app`: the connect screen, the Nostr signaling client,
    `RTCPeerConnection`, and the bridge to the SW (`postMessage` /
    `MessageChannel`).
  - `sw.ts`: the Service Worker. It catches every `fetch` event in its
    scope, forwards it to the active tunnel page, and returns the answer
    as a `Response` (streaming).
- **`agent/`** — Go, a single binary (Windows/macOS/Linux, goreleaser).
  `pion/webrtc` + `nbd-wtf/go-nostr` (or an equivalent). A reverse proxy
  to the local targets.
- **`protocol/`** — the message format on the data channel (below). The
  same constants on the TS and the Go side.
- **`.github/workflows/`** — `web/` → a GitHub Pages deploy; `agent/` →
  the release binaries.

> Why Go rather than .NET? A single static binary, `go install`,
> Homebrew/Scoop distribution and the pion ecosystem. If .NET is wanted, a
> second agent can be written with SIPSorcery, but the MVP is Go.

### Why the Service Worker is needed, and its limits

- Without the SW the visitor page would be nothing but "a viewer that
  shows HTML in an iframe"; with the SW, relative links, CSS, JS, images
  and XHR/fetch — **everything** — works transparently.
- There is no WebRTC inside a SW → the **page** holds the WebRTC
  connection and the SW sends each request to the page with
  `postMessage`. With the tunnel page closed, the SW redirects to the
  connect screen.
- There is **one active tunnel at a time** per origin (the SW scope is per
  origin). Several projects are chosen with `#peer-id/project-name`, and
  the target mapping happens on the agent side.
- The SW cannot catch the local app's own WebSockets (the `fetch` event
  does not cover the WebSocket handshake) → a separate bridge in phase 6:
  with `--inject-ws-shim` a shim that wraps `window.WebSocket` is injected
  into the page and the payload travels over the data channel
  (`docs/WEBSOCKET.md`).
- Search engines do not see the content. This is a demo and sharing tool;
  SEO is not a goal.

## 3. The protocol (the data channel)

Two data channels: `ctrl` (ordered, reliable, JSON) and `data` (ordered,
reliable, binary). The alternative was a single channel with a 1-byte type
prefix; two channels read better for the MVP.

**ctrl (JSON):**
```jsonc
{ "t": "req",  "id": 17, "method": "GET", "path": "/api/items?x=1", "headers": {"accept": "..."}, "hasBody": false }
{ "t": "res",  "id": 17, "status": 200, "headers": {"content-type": "text/html"} }
{ "t": "end",  "id": 17 }                 // the body is over
{ "t": "abort","id": 17, "reason": "client-cancel" }
{ "t": "ping" } / { "t": "pong" }
{ "t": "hello","v": 1, "targets": ["default","admin"], "auth": "required|none" }
```

**data (binary):** `[4 byte id BE][4 byte seq BE][payload ≤ 16 KiB]` — the
same framing for the request body and the response body. The 16 KiB limit
is for browser data channel compatibility; apply backpressure with
`bufferedAmountLowThreshold`.

The rules:
- Hop-by-hop and origin-bound headers such as `host`, `cookie`,
  `connection` and `transfer-encoding` are stripped or rewritten on the
  agent side.
- The agent proxies **only** to the targets given on the command line;
  deriving a host from the path is forbidden (an SSRF guard).
- `http://localhost:3000/...` values in the `Location` header are
  rewritten to the visitor's origin.
- The responses are streamed in the SW as a `ReadableStream`; the full
  body is never awaited.

## 4. Signaling (Nostr)

- At startup the agent generates a Nostr keypair (or reads one from
  `~/.peerpages/key`, stored as an nsec). **peer-id = the FULL npub** — it
  rides after the `#` in the URL.
  - Corrected in phase 2: it used to say "a short form of the npub (the
    first 12 characters)", but NIP-44 encryption and the `#p` tag need the
    complete 32-byte pubkey; it cannot be derived from a shortened id, and
    pubkey prefix search on relays is not reliable. **The readable form is
    on screen**: `npub1yl4dshp…pdnn` is shown while the URL carries the
    whole thing. The reasoning: section 8.
- The visitor page generates an ephemeral keypair too.
- The event kind: ephemeral kind **25050**. The content is encrypted with
  **NIP-44** and the `#p` tag holds the recipient pubkey. The message
  types: `offer`, `answer`, `candidate`, `bye`.
- **The visitor publishes the offer and the agent returns an `answer`.**
  Corrected in phase 2: it used to say "the agent publishes the offer",
  but the agent cannot know the visitor's ephemeral pubkey in advance and
  so cannot send the first message (it would not know who to encrypt to).
  With this direction the message list stays exactly as it was and the
  connection comes up in 2 legs rather than 3. The visitor opens the data
  channels too. The normative definition: `protocol/signaling.md`.
- The default relay list (the user can change it with `--relay` or
  `?relay=`), **verified live in phase 2 with `peerpages doctor`**:
  `wss://nos.lol`, `wss://relay.primal.net`, `wss://nostr.mom`,
  `wss://relay.damus.io` (all four accepted an ephemeral kind 25050 event
  and delivered it to the subscriber). `wss://relay.nostr.band` was
  dropped from the list: the WebSocket handshake timed out from the
  development network. We keep four so that losing one still leaves 3.
  `doctor` marks relays that want NIP-42 auth and we skip them.
- **Several targets (phase 3):** the target name rides in the URL as
  `#npub1…/admin` and is put into the signaling `offer`. One target per
  session: it does not change once the tunnel is up. The list of target
  names is also announced in the ctrl `hello` message once the tunnel
  opens. An unknown target is refused (`bye`/`unknown-target`).
- An optional shared secret: if the agent is started with `--secret` the
  URL becomes `#peer-id:secret`; the secret encrypts the signaling content
  **beneath NIP-44** as an extra layer (HKDF-SHA256 +
  ChaCha20-Poly1305) and the agent refuses offers that are unsealed or
  sealed with the wrong secret. What the secret **does not** protect is
  written down too: relays still see "this npub received something".
  Details: `protocol/signaling.md`.

## 5. The phases

When a phase is **complete**: the tests are green, `CHANGELOG.md` is up to
date, and the commit message is in the `faz-N: ...` format. Do not move to
the next phase before the current one is finished.

### Phase 0 — the skeleton (half a day)
- The monorepo: `web/`, `agent/`, `protocol/`, `docs/`,
  `.github/workflows/`.
- `web/`: Vite + TS, with `base` in `vite.config.ts` settable for the
  GitHub Pages subpath. `sw.ts` is a separate entry (**do not use**
  `vite-plugin-pwa`, build the SW by hand — full control is needed).
- `agent/`: `go mod init`, a `cobra` CLI, the skeleton of
  `peerpages serve --target http://localhost:3000`, logging.
- Lint and format: `biome` (web), `golangci-lint` (agent). `make dev`,
  `make test`.
- The MIT licence and a draft `CONTRIBUTING.md`.

**Acceptance:** `make dev` opens the web app on `localhost:5173` and the
agent's `--help` works.

### Phase 1 — the core proof: manual signaling plus the SW proxy (1–2 days)
Do signaling by **copy-paste** (no server, no Nostr): the agent prints the
`offer` to stdout, it is pasted into the visitor page, the page produces
an `answer`, and that is pasted into the agent. The point: to prove the SW
plus data channel plus reverse proxy chain really works.

- Implement the `protocol/` framing in TS and Go, with unit tests on both
  sides.
- The SW: catching `fetch`, forwarding to the page over a
  `MessageChannel`, answering with a `ReadableStream`.
- The agent: a `net/http` reverse proxy, header sanitizing, body chunking,
  backpressure.
- Put a static site plus a small JSON API into `agent/testdata/site/` as
  the test target.

**Acceptance:** in the visitor's browser the test site (HTML, CSS, JS, an
image, JSON over fetch) renders in full on a github.io-like origin; a 5 MB
file download works; on a refresh, with no tunnel, the SW falls back to
the connect screen.

### Phase 2 — Nostr signaling (1–2 days)
- The agent: the keypair, the relay connection, publishing the `offer`,
  listening for `answer`/`candidate`, NIP-44.
- The web app: the TS side of the same flow (`nostr-tools`). `#peer-id` is
  read from the URL.
- Trickle ICE: send the candidates as separate events.
- Relay selection, a timeout (30 s), and error screens ("the agent is
  offline", "the relays could not be reached").
- `--secret` support.

**Acceptance:** two devices on different networks (for example a phone on
mobile data and a laptop on home Wi-Fi) connect with the URL alone; the
manual signaling path moved behind the `--manual` flag and Nostr is the
default.

### Phase 3 — several targets and the visitor experience (1 day) — **done**
- `--target default=http://localhost:3000 --target admin=http://localhost:4000`;
  the URL `#peer-id/admin`.
  - The target name rides in the signaling `offer`
    (`protocol/signaling.md`); it is fixed per session. An unknown target
    is **refused** with `bye`/`unknown-target` — there is no silent
    fallback to `default`.
  - On the manual signaling path there is no URL, so `--manual-target`
    picks the target.
- The connect screen: the status (SW → signaling → ICE → tunnel), the
  latency measured **continuously** (a ping every 5 s, the last value plus
  the median), requests and errors, the bytes received and sent, the
  current rate, the target list, the active target, a "copy" button and a
  QR code.
  - The QR encoder was **written by hand** (`web/src/ui/qr.ts`, byte mode
    / level M / versions 1–10), so the web side keeps a single runtime
    dependency (`nostr-tools`). It is verified against vectors produced by
    a third, independent implementation.
- The target list through the `hello` message; per-target request, success,
  error and byte counters on the agent side (`agent/internal/stats`), and
  a status line on stdout that only refreshes when something changes.
- Resources are released when the visitor closes the tab: **the agent**
  sends a ctrl `ping` every 10 s and closes the session if no answer comes
  within 30 s; **the SW** re-probes a stale tunnel owner. The reasoning
  and the limits: section 8.

**Acceptance:** a single agent shares two projects and the screen carries
meaningful status information.
**Status:** met — `web/e2e/phase3.spec.ts` (6 tests) verifies it
automatically.

### Phase 4 — distribution and resilience (1 day) — **done**
- Automatic reconnects (a relay going down, sleep/wake).
  - The visitor side builds **a new session from scratch** with a backoff
    (1→2→4→8→15→30 s): a new `sid`, a new ephemeral keypair, a new offer.
    **No ICE restart** — the reasoning is in section 8.
  - `online` and `visibilitychange` reset the backoff (the wake path). The
    liveness check: two unanswered pings in a row count as a drop.
  - The agent side: ICE `Disconnected` is now met with a 10-second grace
    window, the idle check runs on a monotonic clock and recognises a wake
    from its own lateness, the signaling subscription is supervised and
    renewed, and a `relay 3/4` field appears in the status line.
  - During a drop the Service Worker does not 503 a request right away:
    subresources wait 10 s and navigations 3 s; a GET/HEAD caught by a
    drop mid-request is retried once.
- goreleaser: Windows/macOS/Linux × amd64/arm64,
  `go install github.com/aliosmanmho/PeerPages/agent/cmd/peerpages@latest`
  (phase 8 added the `cmd/peerpages` path: the older `…/agent@latest`
  installs the binary as `agent`, see `docs/INSTALL.md`), and a
  Scoop/Homebrew tap **draft** (the tap repositories do not exist and
  uploading is off). The release opens as a **draft**.
  `peerpages version` prints the version, commit, date and platform; a
  binary installed with `go install` knows its version too (without
  ldflags).
- `peerpages key` and `peerpages doctor` arrived in phase 2;
  `docs/INSTALL.md` covers end-user installation.
- A note on running it as a service on Windows (NSSM/Task Scheduler) —
  `docs/INSTALL.md`, documentation only.

**Acceptance:** the tunnel recovers when the laptop sleeps and wakes; the
release workflow produces binaries for all three platforms.
**Status:** the reconnect is met — `web/e2e/phase4.spec.ts` (4 tests)
verifies the agent restarting, a relay going down and coming back, and the
drop window automatically; **a real sleep/wake has to be tried by hand**
(`docs/MANUAL-TEST.md`). The binary side was verified locally (6 archives
plus checksums); **the workflow has not been run on GitHub**, because
nothing was pushed or tagged (section 8).

### Phase 5 — the TURN fallback (half a day) — **done**
- With `--turn <url> --turn-user --turn-pass` the user can pass their own
  TURN (`PEERPAGES_TURN_PASS` is read too: command line arguments show up
  in `ps` output). `--ice-relay-only` makes the direct path impossible,
  for diagnostics.
- **TURN is on the agent side only**; no credential is ever sent to the
  visitor. The reasoning: section 8.
- Cloudflare Realtime TURN: the agent **fetches** the short-lived
  credentials **from the API itself** and refreshes them before they
  expire (`--turn-cloudflare-key-id`, `PEERPAGES_CF_TURN_TOKEN`).
  **No Worker was written** — the reasoning is in section 8; "the one
  piece that needs a server" went away.
- `doctor` **asks every TURN server for an allocation** ("does it give me
  a relay address" rather than "is it up") and prints one line of advice
  that joins that with the NAT verdict.
- The connect screen shows the chosen route: direct or over TURN
  (`pc.getStats()`).
- STUN reached the visitor side too: until this phase the page gave **no**
  ICE server at all (it only gathered host candidates, section 8).
- Setup and security notes: [`docs/TURN.md`](TURN.md).

**Acceptance:** it connects over TURN in a symmetric NAT simulation (for
example a mobile hotspot ↔ a corporate network).
**Status:** met — when the agent gathers relay candidates only with
`--ice-relay-only`, the direct path is **impossible**;
`web/e2e/phase5.spec.ts` (4 tests) and
`agent/internal/connect/turn_test.go` verify that the tunnel opens in that
case and does not in the negative control (no TURN). The real two-network
attempt and the Cloudflare account are yours (`docs/MANUAL-TEST.md`,
`docs/TURN.md`).

### Phase 6 — the WebSocket and SSE bridge (1 day) — **done**
- SSE should already work with a streaming response; add a test.
- WebSocket: a small shim injected into the page wraps
  `window.WebSocket` and bridges to a real WS on the agent side with
  `ws-open/ws-frame/ws-close` messages over the data channel. The shim is
  only turned on with `--inject-ws-shim` (we interfere with the HTML, so
  it is off by default).

**Acceptance:** the Vite HMR and Socket.IO examples work over the tunnel.
**Status:** Vite HMR is **met and automated** —
`web/e2e/phase6.spec.ts` starts `vite dev` in a fixture project and
changes a source file, and the page updates **without reloading** (38 ms).
That SSE arrives incrementally is measured now too (the first event at
2 ms, the third at 804 ms); until phase 6 the agent's 60-second timeout
wrapped the whole response and cut long streams off silently. The
Socket.IO example is **manual** (`docs/MANUAL-TEST.md`): it was not tested
automatically because it would bring two new dependencies. Details:
`docs/WEBSOCKET.md`.

### Phase 7 — publishing and the launch (1 day) — **done**
- The GitHub Pages deploy workflow (`web/dist` → Pages, with Actions as
  the source) and a custom domain guide (`CNAME`,
  `demo.yourdomain.com`).
- A public **English README**: the 10-second GIF, a "Why" section, the
  comparison table, the security model, the limits (honestly) and an FAQ.
- `docs/SECURITY.md`: the threat model (guessing a peer-id, relay
  observation, SSRF, the visitor may be malicious).
- `docs/LAUNCH.md`: the Show HN title plus the first comment, the
  r/selfhosted and r/webdev texts, and the short X/LinkedIn text.
- The final name decision and the domain check.

**Acceptance:** a stranger can read the README and share their own
localhost in 3 minutes.
**Status:** met and **measured live**. The site is up
(<https://aliosmanmho.github.io/PeerPages/>); the agent was run with no
flags, the URL it printed opened as it was, the tunnel came up in **8.6
seconds** and the local site appeared under the github.io address (a small
request 2 ms, 5 MB in 114 ms — against real Nostr relays). Details:
`docs/MEASUREMENTS.md`, phase 7.
**Note:** because Actions could not run under the account's billing lock,
publishing went through the **branch-based** Pages path (`gh-pages`)
rather than `.github/workflows/pages.yml`. The workflow is still there and
the source can be switched back to "GitHub Actions" with one click once
the lock is gone (section 8).

### Phase 8 — an English project plus a multilingual page (1 day) — **done**
- **Every document is English**, with no Turkish copy kept (a single
  source).
- **Multilingual on the web page only**: English is the default and
  Turkish can be chosen. The agent's CLI output, error messages and logs
  are **English only** — a translated log breaks searchability and
  debugging.
- **The commit history stays as it is**; new messages are English. No
  force-push: rewriting 57 SHAs would kill the `gh-pages` history and
  every link handed out.
- The GitHub repository settings (description, topics, PR settings,
  community files) and a guard script against leftover Turkish
  (`scripts/check-language.mjs`).

**Acceptance:** nothing Turkish is left in the code or on GitHub, and the
page speaks two languages with English as the default.
**Status:** met — `web/e2e/i18n.spec.ts` verifies the English default,
`?lang=tr`, the fallback for an unknown `?lang=` and that the picker
survives a reload; the guard script reports zero. **Verified LIVE** after
the merge: a first-time visitor to
https://aliosmanmho.github.io/PeerPages/connect/ gets `<html lang="en">`
and "Serverless tunnel", `?lang=tr` turns the whole screen Turkish and the
choice is remembered, and `?lang=zz` falls back to English with a console
warning instead of an error. Publishing again went through the `gh-pages`
branch (26 s), because Actions is still billing-locked.

## 6. The security principles (they hold in every phase)

1. The agent never makes a request to a host that was not given on the
   CLI.
2. Signaling content is always encrypted; relays only see "a pubkey
   received something".
3. The peer-id has at least 64 bits of entropy; `--secret` is an extra
   layer.
4. The visitor side is treated as untrusted: path and header validation, a
   body size limit (`--max-body 50MB`) and a request count limit.
5. The SW only catches requests in its own scope; it never touches other
   origins.
6. By default the logs mask URL paths (`--verbose` discloses them).

## 7. The test strategy

- **Unit:** the framing (TS and Go), header sanitizing, Nostr event
  encode/decode, NIP-44.
- **E2E:** Playwright (Chromium) — start the agent and the test site,
  connect with manual signaling, and verify the page render plus fetch
  plus a download plus SSE. It runs on every PR in CI.
- **The manual matrix (before every release):**
  Chrome/Edge/Firefox/Safari (iOS included) × a Windows/macOS/Linux agent.
  Check the streaming SW response behaviour in Safari and Firefox
  specifically.

## 8. The known risks and open questions

- **(Measured in phase 1, CLOSED) There is no ordering guarantee between
  the `ctrl` and `data` channels.** Two data channels are two separate
  SCTP streams; each is `ordered` within itself but there is no ordering
  between them. The measurement (pion loopback, a 90 KB response): six
  body frames arrived over `data` **after** the `end` message had arrived
  over `ctrl`. So the receiver cannot read `end` as "the body is over";
  under the definition of the time the response would have looked empty.
  **The chosen solution:** the `end` message now carries `frames` and
  `len` counters (`{"t":"end","id":17,"frames":6,"len":90000}`). When the
  receiver sees `end` it keeps reading `data` until it reaches the
  expected frame and byte counts; more than that is an `abort`
  (`body-length-mismatch`). The same problem existed in the request
  direction (body frames can arrive before `req`): the agent holds frames
  whose owner is unknown in a short-lived buffer and processes them in
  order once `req` arrives. The two-channel, 16 KiB framing and
  `end`-on-ctrl decisions were all kept. Details: `protocol/README.md`.

- **(Found in phase 1, CLOSED) The app shell and the local site's root
  both want the same URL.** The connect screen and the tunnel-owner page
  sat at the scope root (`/peerpages/`); the local site's root has to be
  in the same place for relative links to work. The two cannot be at the
  same URL: in the e2e test a request for `/peerpages/index.html` landed
  on the app shell and the connect screen showed up instead of the test
  site. And if the tunnel-owner page sits at the scope root, refreshing
  that tab sends its own URL through the tunnel and the tunnel closes
  itself.
  **The chosen solution:** the connect screen and the tunnel-owner page
  live at `/peerpages/connect/`; the scope root (`/peerpages/`) is the
  local site's root. The tunnel-owner page's URL never goes through the
  tunnel, so the tunnel survives even when that tab is refreshed. On the
  first visit (with no SW yet) the scope root serves a small bootloader:
  it installs the SW and moves on to `connect/`, preserving the hash. The
  `#peer-id` URL form from the README was kept.

  Note: as long as the scope is under a subpath, the local site's
  ROOT-ABSOLUTE paths (`/style.css`) fall outside the scope in every case
  and the SW cannot catch them. That is a structural boundary of GitHub
  Pages project pages; it goes away with a custom domain or a user page
  (`user.github.io/`). It should be written honestly into the README's
  "limits" section. **(Done in phase 7:** in the public README's "Honest
  limits" section and in the custom domain item of the FAQ.**)**

- SW `ReadableStream` responses and `postMessage` performance in
  Firefox/Safari — **measured in Chromium and there is no problem**: a
  5 MB download takes about 103 ms (about 48 MB/s), the SW bridge adds
  about 1 ms on a small request, and the first byte comes in under 1 ms
  (no temporary buffer was needed). Details: `docs/MEASUREMENTS.md`.
  **Firefox and Safari are still unmeasured**; the phase 1 e2e suite only
  runs Chromium. The manual matrix (section 7) will be done before phase
  4, and the streaming response behaviour in Safari will be checked
  specifically.

- **(Measured in phase 2, CLOSED) The relays' behaviour with ephemeral
  events.** `peerpages doctor` runs NIP-11, a connection and an
  **ephemeral echo** test for every relay: a kind 25050 event is published
  and read back from a subscription. That is the critical one: a relay can
  accept an event with "OK" and drop it silently, and then signaling does
  not work while no error shows up. The 2026-10-26 measurement: four
  relays are usable and `relay.nostr.band` could not be reached from this
  network. The list was updated accordingly.

- **(Measured in phase 2, OPEN — TURN may be needed) The NAT on this
  development network is symmetric.** `doctor` compares the external port
  two STUN servers see; the output differed, so the mapping is
  endpoint-dependent. If the other side is behind symmetric NAT too, no
  direct WebRTC connection can be made and **TURN is needed (phase 5)**.
  This can surface while trying phase 2's "two devices on different
  networks" acceptance criterion; `doctor` now says so in advance. On one
  machine and on the same network there is no problem.

- **(Found in phase 2, CLOSED) The agent has to really be listening before
  it says "ready".** It surfaced in an e2e run against real relays: the
  agent reported it was ready before the subscription was up, and the
  offer of a visitor who opened the URL right away was lost **with no way
  back** (ephemeral events are not stored). Because the fake relay is on
  localhost, none of this was visible. The order now: connect to the
  relays → subscribe → say "ready".

- **(Found in phase 3 and CLOSED) A body over the limit arriving before
  `req` left the request hanging.** Because `ctrl` and `data` are separate
  SCTP streams there is no ordering guarantee between them (measured in
  phase 1, above). When a body frame arrived before `req` and exceeded the
  `--max-body` limit, the frame was **dropped silently**: because the body
  never arrived the proxy waited forever, the visitor got no `abort` and
  the request hung — so a limit guard turned into a resource leak. A flaky
  test surfaced it (3 red in 6 runs); the problem was not the test but the
  behaviour itself. Now the violation is flagged, an
  `abort body-too-large` goes to the visitor immediately, and the request
  never reaches the proxy.

- **(Closed in phase 3, with the SW side deliberately limited) Releasing
  resources once the visitor is gone.** There were two separate gaps.
  (a) The agent had no idle check at all: a session only closed when the
  ICE/DTLS state changed, which can take far longer than 30 seconds, and
  `Disconnected` may come back. The agent now sends a ctrl `ping` every
  10 s and closes the session if no answer comes within 30 s (the protocol
  did not change: the page already answered an incoming ping with a
  `pong`). (b) In the SW, `clients.get` returned a client as long as the
  tab was **alive**, even after it had lost its tunnel; and the notice the
  page sends on `pagehide` is best effort (if the tab crashes it never
  arrives). A stale tunnel owner is re-probed now.
  **The honest limit:** the detection on the SW side is **lazy** — with no
  request arriving there is no check either. The real, time-bounded
  protection is the check in the agent. A periodic timer in the SW is not
  reliable: a worker gets killed while idle.

- **(Phase 3, OPEN) That the QR really scans has to be verified by hand.**
  The encoder is verified byte for byte against a third, independent
  implementation's vectors and the canvas being drawn is checked in e2e,
  but "does a phone camera read this" cannot be answered by an automated
  test. The method: `docs/MANUAL-TEST.md` → "Trying it with two devices".

- **(Phase 3, OPEN — external) Starting agents back to back in one run
  against real relays.** With two real relays passed through
  `PEERPAGES_E2E_RELAYS`, one of the tests fails with "no relay could be
  reached": every test starts a new agent, and public relays refuse rapid
  repeat connections from the same address. With a single relay (nos.lol)
  it is 7/7 green. It is not from our code; the reconnect work in phase 4
  may improve it too.

- **(Decided in phase 4 — A DEPARTURE from the README) The reconnect
  builds a new session from scratch rather than doing an ICE restart.**
  The phase 4 item said "ICE restart"; it was discussed and changed. Two
  reasons: (a) the agent is answerer-only and a session is one-shot
  (`remoteSet` is a one-way latch), so accepting a second offer into the
  same session would have needed renegotiation on both sides and new
  signaling rules; (b) the 30-second idle check added in phase 3 already
  closes the agent-side session on a long drop — so the window an ICE
  restart could rescue is drops shorter than 30 seconds. Every attempt
  builds from scratch with a new `sid` and a new ephemeral keypair. The
  cost: requests in flight at the moment of the drop are lost (the SW
  retries the idempotent ones once).

- **(Found in phase 4 and CLOSED) After a wake the idle check was killing
  sessions.** `lastSeen` was stored as unix nanos and read back with
  `time.Unix(...)`; a `time.Time` built that way **does not carry the
  monotonic reading**, so the comparison turned into a wall clock one. The
  suspended time counted as "silence" as it was, and the first tick after
  the wake closed every session with `ErrIdle` — even with the visitor
  still there, without a single ping attempt. And if the clock went
  backwards the duration went negative and the check quietly switched
  itself off. The duration is measured monotonically now; on top of that
  the check looks at **its own lateness**: if the gap between two ticks is
  more than 3 times the expected one, that is a wake, the silence counter
  is reset and the session gets one more round of pings. Whether a
  monotonic clock counts suspended time varies by platform (Linux
  `CLOCK_MONOTONIC` does not, Windows does) — which is why both guards
  stand together.

- **(Found in phase 4 and CLOSED) ICE `Disconnected` counted as a clean
  close.** The state is temporary and pion often returns on its own; the
  code's own comment said so, yet the handler closed the session instantly
  and reported it with `closeWith(nil)`, indistinguishably from the
  visitor's `bye`. There is a 10-second grace window now, and the reason
  (`idle` / `connection-lost` / `clean`) shows up in the log.

- **(Found in phase 4 and CLOSED) The SW's waiting window missed the
  request caught by the drop.** The first implementation only waited in
  the "there is no tunnel-owner tab" case. If the tunnel dropped
  mid-request the request was still dispatched, errored on the page and
  the visitor saw a **502** — so the window never applied on that path.
  The e2e test caught it. Now, if the response head never went out and the
  tunnel closed, the page marks the error as "retryable"; the SW waits for
  the reconnect and retries **once** — for GET/HEAD only, because the SW
  cannot know whether a POST reached the target.

- **(Phase 4, OPEN) The release workflow has not been run on GitHub
  Actions.** Nothing was pushed and nothing was tagged (your rule).
  Verified locally: `goreleaser check` is clean,
  `goreleaser release --snapshot --clean` produced six archives plus
  `checksums.txt`, and the resulting Windows binary printed the right
  version. Unverified: that `.github/workflows/release.yml` really runs,
  that the draft release opens, and that the assets get uploaded. It will
  be seen on the first `v*` tag.

- **(Phase 4, OPEN) If the agent process crashes nobody brings it back.**
  The agent recovers from a relay going down and from sleep/wake, but
  there is no supervisor to restart the process after a panic; the README
  deliberately marked this "documentation only". `docs/INSTALL.md`
  explains the Task Scheduler (with a restart policy) and NSSM paths.

- **(Decided in phase 5) TURN is on the agent side only.** Neither the
  TURN address nor any credential is sent to the visitor. The forcing
  reason is structural: the visitor produces the offer **before** hearing
  a single word from the agent (the role swap, above), so sending TURN
  details over signaling would come far too late. One-sided TURN is
  enough: the agent gathers a relay candidate, the visitor sends packets
  outward to that public address — because there is a single destination
  the mapping is consistent even with symmetric NAT — and the agent
  recognises it as peer-reflexive. The gain: the quota and the secret stay
  with their owner (principle 4), the address stays short, and no secret
  travels through a QR or the clipboard. The cost: the exotic cases where
  the visitor needs a relay of their own are out of scope (the upgrade
  path is open).

- **(Decided in phase 5 — A DEPARTURE from the README) No Cloudflare
  Worker was written.** The phase 5 item called for a 30-line Worker under
  `cloudflare-turn/`; it was discussed and cancelled. The Worker's only
  job was to hide the API token **from the browser** — and since only the
  agent uses TURN and the token already sits on the owner's machine, the
  server in between is unnecessary. The agent fetches the credentials from
  the API itself and refreshes them at 80% of the TTL. That way **"the one
  piece that needs a server" went away entirely**: the zero-server claim
  holds even with TURN.

- **(Found in phase 5 and CLOSED) The visitor page gave no ICE server at
  all.** `new RTCPeerConnection(undefined)` — so the browser gathered
  **host candidates** only, and a cross-network connection relied on the
  agent learning the visitor peer-reflexively from a packet arriving at
  its srflx candidate. The `ConnectOptions.iceServers` field existed but
  had no caller (dead code). The default STUN list is passed now
  (`web/src/ice.ts`, the same as `ice.DefaultSTUN` on the Go side) and the
  visitor gathers its own srflx candidate — which on its own means a
  direct connection on more NATs, with no TURN needed.

- **(Phase 5, OPEN) Cloudflare TURN has not been tried against a real
  account.** The request shape and the response parsing are verified with
  `httptest`; the live verification is on the user's side
  (`docs/TURN.md`). If the API's response shape differs from what is
  documented, the agent fails to start on the first attempt with a clear
  error (it does not quietly carry on without TURN).

- **(A phase 5 note) `--ice-relay-only` is a diagnostic flag.** It makes
  the direct path impossible; that is the only way to answer "did the
  connection really go over TURN" deterministically, and the automated
  test of the acceptance criterion rests on it. It is not needed in
  production: ICE prefers the direct path anyway (measured, see
  MEASUREMENTS).

- **(Learned in phase 5) The address a fake TURN server advertises has to
  be the same as the source address it uses when it sends packets.** The
  first version of `faketurn` bound the relay to 127.0.0.1; because the
  other side (pion and browsers) produces no loopback candidate, the relay
  could not send packets there. Then, with the socket bound to every
  interface while the address was advertised as 127.0.0.1, the browser
  counted the incoming check as **peer-reflexive** and the selected
  candidate pair did not look like a relay — so the test could not measure
  a TURN that worked correctly. On a real TURN server that consistency is
  there already; the fake server advertises the LAN address now.

- **(Decided in phase 6 — A DEPARTURE from the README) The Service Worker,
  not the agent, injects the shim into the HTML.** The phase 6 item says
  "a shim injected into the page" and puts the flag on the agent; the flag
  stayed on the agent but **the SW does the injection**. The agent only
  adds an `x-peerpages-ws-shim: 1` header to HTML responses and never
  touches the body. Three reasons: (a) the side that knows the scope
  prefix (`/PeerPages/`) is the SW, not the agent — the agent cannot
  produce an absolute script path; (b) `internal/proxy` thereby stays
  **byte-for-byte transparent** and the tests that check it stay valid;
  (c) the alternative was announcing the capability in the `hello`
  message, but `hello` is one way, refuses `v != 1` outright, and the
  canonical form has a "no optional fields" rule — so the protocol version
  would have had to break. The policy lives in the agent and the
  mechanics in the SW. Details: `docs/WEBSOCKET.md`.

- **(Found in phase 6 and CLOSED) A 60-second timeout was cutting long
  streams off silently.** `internal/proxy` wrapped the request context in
  `context.WithTimeout(ctx, 60s)` and that covered **the whole response**.
  Every SSE stream living longer than 60 seconds (and Socket.IO's long
  polling) was cut off; the visitor saw no error, just "the stream ended" —
  a silent breakage. The bound now lives in the `http.Transport` and
  covers **the header phase only** (`ResponseHeaderTimeout`); while the
  body streams there is no bound. A target that never answers still cannot
  hold a request forever (the test:
  `TestServeHeaderPhaseStillTimesOut`).

- **(Phase 6, OPEN — an honest limit) The long-lived stream budget is 8.**
  `--max-streams` bounds ws sessions **and** SSE responses together; when
  the budget is full an SSE request gets `abort too-many-streams` and a ws
  request gets `ws-close 1013`. Why a separate budget: a long-lived stream
  holds its `--max-concurrent` slot for its whole life, so with a single
  budget 64 SSE requests would make the tunnel unusable for ordinary
  requests. We **cannot know in advance** which response is long-lived: we
  reclassify the moment `content-type: text/event-stream` is seen, which
  is the only honest hint. An application that uses many concurrent SSE
  streams has to raise that number.

- **(Phase 6, OPEN) The `Origin` header goes out as the target's own
  origin.** Local servers (Vite, Socket.IO) check `Origin` during the
  WebSocket handshake and would refuse the visitor's `github.io` origin.
  So the tunnel looks like **a local client** to the local server. The
  cost is clear: the local server's origin check cannot tell traffic from
  the tunnel apart. Because the bridge is off by default this is a
  deliberate trade, and it should go into phase 7's `SECURITY.md` as a
  threat model entry.

- **(Phase 6, OPEN) Socket.IO was not tested automatically.** Of the two
  halves of the acceptance criterion, Vite HMR really runs (`vite` is
  already a devDependency); Socket.IO would bring the `socket.io` plus
  `socket.io-client` dependencies, so it sits in the manual verification
  (`docs/MANUAL-TEST.md`). The polling phase is ordinary HTTP, the upgrade
  goes through the shim, and binary payloads are supported — but the
  claim "it works" has not been measured.

- **(Phase 6, OPEN) `bufferedAmount` is always 0 and there is no extension
  negotiation.** The real send buffer is inside the tunnel; having the
  shim imitate the value would be misleading. The subprotocol passes
  through while extensions such as `permessage-deflate` do not
  (`extensions` is empty). If the tunnel drops, open sockets go down with
  1006; the reconnect is left to the page's own library.

- **(Phase 7, OPEN — external) GitHub Actions is locked on the account;
  publishing went through the branch-based Pages path.** After `main` was
  pushed, both the `Pages` and the `CI` workflow failed to start with
  *"The job was not started because your account is locked due to a
  billing issue"* (`CI` hung in the queue and was cancelled). As a
  workaround the site was published **with GitHub's own Pages builder**:
  the `gh-pages` branch plus `build_type: legacy`. That pipeline does not
  use Actions and does not hit the lock — it built and published in 27
  seconds.
  **The upshot:** the site is live but `.github/workflows/pages.yml` and
  `ci.yml` have **never run** on a GitHub runner; so `-race` and the CI
  version of `golangci-lint` have only been tried locally (the same family
  as the "the release workflow has not been tried" item, open since phase
  4). What to do once the lock is gone: switch the Pages source back to
  "GitHub Actions"
  (`gh api -X PUT repos/.../pages -f build_type=workflow`), delete the
  `gh-pages` branch, and `gh workflow run Pages`. The cost of the
  branch-based publish: every update has to be built and pushed to
  `gh-pages` by hand.

- **(Measured in phase 7) The acceptance criterion is met, but there is no
  binary download path.** The live chain worked (8.6 s,
  `docs/MEASUREMENTS.md`). Against that, with no tag or release the only
  installation path is `go install`: for someone without Go installed the
  "3 minutes" claim is **not valid yet**. Pushing the `v0.1.0` tag (once
  release.yml can run) closes that.

- **(Decided in phase 7, CLOSED in phase 8) The interface and the
  documents stayed Turkish.** In phase 7 only the public README and
  `docs/SECURITY.md` were English; the agent's CLI output, the connect
  screen and the remaining documents were Turkish, and the README said so
  under "honest limits". Phase 8 closed it: every document, every comment
  and every user-facing string is English now, and the connect screen
  speaks two languages (English by default, Turkish selectable). The
  agent's CLI stayed English-only on purpose — a translated log breaks
  searchability and debugging.

- **(Phase 7, OPEN) The launch texts have not been posted.**
  `docs/LAUNCH.md` keeps the Show HN, r/selfhosted, r/webdev and social
  media texts ready; the decision to post, and when, is the user's. The
  company account name in the X/LinkedIn text is a placeholder.

- **(Phase 8, OPEN — an honest limit) The Service Worker's four plain-text
  bodies stayed English.** The four messages the SW writes when there is
  no tunnel (`src/sw.ts`) are not in the dictionary: the SW is a separate
  build target, it has no DOM, and it does not know the language the page
  chose. Moving the dictionary into the SW bundle would mean bundle size
  and a synchronisation burden for four rarely seen strings. Navigations
  are redirected to the connect screen anyway, so that screen is the
  surface that really gets translated.

- **(Phase 8, OPEN — a deliberate departure) An unknown `?lang=` falls
  back instead of throwing.** `relaysFromSearch` and `stunFromSearch`
  throw on invalid input, because an incomplete relay list means no
  tunnel. An unsupported language is only cosmetic, so dropping the page
  into an error screen would be out of proportion: `resolveLang` falls
  back to English and writes a line to the console.

- **(Found in phase 8, OPEN) Our QR mask choice and python `qrcode`'s do
  not always agree.** Translating the vector data meant changing the
  sample visitor URL, and with some hosts of the same length the two
  implementations pick a different mask for the SAME input, so the `auto`
  vectors stopped matching. Any mask produces a readable code, so this is
  quality, not correctness: our penalty scoring disagrees with the
  reference implementation in some edge case. The vector data was left at
  a host where the two agree (`localhost.github.io`) rather than chasing
  it inside a translation phase; a separate phase should compare the four
  penalty rules against the reference.

- Some relays may drop Nostr ephemeral events → relay health through
  `doctor`; the list is kept current.
- The GitHub Pages terms of use: fine for a static showcase; do not
  present the project as a commercial proxy, and call it a "demo and
  sharing tool" in the README.
- The agent is Go; if a .NET agent is wanted it becomes a separate phase
  as `agent-dotnet/`.

## 9. The technology choices (a summary)

| Layer | Choice | Alternative |
|---|---|---|
| Web | Vite + TypeScript, no framework | Preact (if the UI grows) |
| SW | A hand-written `sw.ts` | — |
| Nostr (web) | `nostr-tools` | — |
| Agent | Go 1.27+, `pion/webrtc`, `go-nostr`, `cobra` | .NET + SIPSorcery |
| Release | goreleaser, GitHub Actions | — |
| Tests | Vitest, Playwright, `go test` | — |

---

## 10. The starting text given to Claude Code

> **A historical record.** This text was written on the project's first
> day, while this file was still the `README.md` at the repo root, and it
> is kept as it was: it is what explains how phases 0–7 were run. Someone
> doing the same job today should read the file name as `docs/BRIEF.md`.
>
> It is kept in the original Turkish deliberately: it is a record of what
> was said, not documentation to follow.

Paste the text below to Claude Code at the repo root (together with this
brief file):

```
Bu repodaki README.md projenin brief'i ve yol haritasıdır; önce tamamını oku.

Görev: Faz 0 ve Faz 1'i tamamla. Kurallar:
1. Başlamadan önce plan moduna geç, README'deki Faz 0 + Faz 1'i somut adımlara böl ve bana göster; onayımdan sonra uygula.
2. README'deki kararları (Go ajan, Vite+TS, framework yok, elle yazılmış Service Worker, iki data channel, 16 KiB framing, güvenlik ilkeleri) değiştirme. Değiştirmek istediğin bir şey olursa önce sor.
3. Faz 1'de signaling sunucusu veya Nostr YOK; offer/answer kopyala-yapıştır ile taşınır. Amaç SW + WebRTC + reverse proxy zincirini kanıtlamak.
4. Her alt adımdan sonra testleri çalıştır; Faz 1 bitince Playwright e2e testi ile kabul kriterlerini doğrula ve bana nasıl elle deneyeceğimi (iki tarayıcı penceresi/iki cihaz) adım adım yaz.
5. Protocol framing'i hem TS hem Go'da birim testli yaz; iki tarafın aynı test vektörlerini kullanmasını sağla (protocol/testvectors.json).
6. Bir karar noktasında takılırsan README'nin "Bilinen riskler" bölümüne not düş ve bana sor; tahmin yürütüp ilerleme.
7. Commit'ler küçük ve açıklayıcı olsun, format: "faz-0: ...", "faz-1: ...". CHANGELOG.md tut.
8. Faz 1 kabul kriterleri karşılanmadan Faz 2'ye geçme.

Bittiğinde: ne yaptığını, nelerin çalıştığını, ölçtüğün gecikme/throughput değerlerini ve Faz 2 için önerilerini kısa bir rapor olarak yaz.
```
