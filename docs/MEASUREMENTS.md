# Measurements

## Phase 1 — 2026-10-26

All of it was taken **on one machine (loopback)**: the browser, the agent
and the site being shared are on the same computer. So these are **baseline
numbers with no network latency in them**: they show the cost of the
protocol and the chain itself, not real-world laptop↔phone figures. The
over-the-network measurement comes in phase 2 (two real devices with Nostr
signaling).

**The environment:** Windows 11, Go 1.27.0, Node 22.12.0, Chromium
(Playwright 1.64), pion/webrtc v4.2.25.

### The two halves of the chain were measured separately

**1) The agent half only** — a real WebRTC connection plus a real HTTP
server, with no browser (the `agent/internal/proxy` integration test):

| Measurement | Value |
|---|---|
| A 5 MB download | 63 ms (about 79 MB/s) |
| Time to the first byte on 5 MB | < 1 ms |
| `ctrl` ping RTT | mean 80 µs, worst 0.5 ms (n=20) |
| A small GET (`/api/items`, 290 bytes) | mean 0.18–0.32 ms (n=20) |

**2) The whole chain** — browser → Service Worker → tunnel page → WebRTC →
agent → localhost (Playwright e2e, three separate runs):

| Measurement | Value |
|---|---|
| A 5 MB download | 101 / 107 / 100 ms → **about 47–50 MB/s** |
| `ctrl` ping RTT (from the page) | median 0.2–0.3 ms (n=10) |
| A small GET (SW + tunnel) | median 1.15–1.35 ms, p95 2.3–2.4 ms (n=20) |

### How to read it

- **The Service Worker bridge has a visible but acceptable cost.** On
  5 MB the agent half takes 63 ms and the whole chain about 103 ms: the
  roughly 40 ms in between is what the `postMessage` plus `ReadableStream`
  layer costs. Throughput drops from 79 MB/s to about 48 MB/s. That is
  more than enough for sharing a development site; in real use the
  bottleneck will be the network.
- **A small request gains about 1 ms.** The agent half is about 0.2 ms and
  the whole chain about 1.2 ms. So the SW bridge adds roughly 1 ms per
  request. For 50 requests a page, about 50 ms; unnoticeable.
- **The first byte arrives right away** (< 1 ms): the body streams and the
  full response is never awaited. On a 5 MB download the whole body never
  piles up in memory.
- **Backpressure works:** 5 MB goes out as 320 frames (16 KiB), and once
  `bufferedAmount` passes the 1 MiB threshold sending waits. No lost
  frames and no broken order were observed.

### Not measured yet (honestly)

- **Firefox and Safari.** The question in BRIEF section 8 about "SW
  `ReadableStream` responses and `postMessage` performance" was answered
  **in Chromium**; Firefox and Safari were not measured. The phase 1 e2e
  suite only runs Chromium. The manual matrix (BRIEF section 7) will be
  done before phase 4; the behaviour of streaming responses in Safari
  needs a separate check.
- **A real network.** Two devices, different networks, latency and
  throughput with STUN/TURN: to be measured together with phase 2's
  acceptance criterion.
- **Concurrent load.** Dozens of parallel requests from one page (loading
  a real SPA) was not measured; the `--max-concurrent` default is 64.
- **A long-lived session.** Memory leaks and sleep/wake are a phase 4
  subject.

### Reproducing the measurement

```bash
# The agent half (no browser)
cd agent && go test ./internal/proxy/ -run TestIntegration -v 2>&1 | grep MEASURED

# The whole chain (Chromium)
cd web && npx playwright test 2>&1 | grep MEASURED
```

---

## Phase 2 — 2026-10-26 (Nostr signaling)

### Relay health (`peerpages doctor`, real relays)

From this development network, against the real public relays:

| Relay | Verdict | Connect | Publish | Echo |
|---|---|---|---|---|
| `wss://nos.lol` | usable | 142 ms | 50 ms | 72 ms |
| `wss://relay.primal.net` | usable | 238 ms | 83 ms | 78 ms |
| `wss://nostr.mom` | usable | 137 ms | 47 ms | 101 ms |
| `wss://relay.damus.io` | usable | 319 ms | 196 ms | 103 ms |
| `wss://relay.nostr.band` | **unreachable** | — | — | — |

"Echo" = a published ephemeral event coming back to the subscriber. That
step matters most: a relay can accept an event with "OK" and drop it
silently.

### NAT / STUN

| | The external address seen |
|---|---|
| `stun.l.google.com:19302` | `91.93.232.50:18450` |
| `stun1.l.google.com:19302` | `91.93.232.50:18836` |

**Different ports → symmetric NAT.** If the other side is behind symmetric
NAT too, this machine cannot connect directly; TURN is needed (phase 5).
On one machine and on the same network it does not matter.

### From the URL to an open tunnel

| Setup | Time |
|---|---|
| A fake relay (loopback) | **356 ms** |
| Real relays (nos.lol + primal + nostr.mom) | **979 ms** |

So the site appears about a second after the visitor opens the URL. That
time is signaling's round trip plus the ICE setup; the relay latency
dominates.

### Data over a tunnel built with Nostr

| Measurement | Value |
|---|---|
| A 5 MB download | 88–93 ms (about 54–57 MB/s) |
| `ctrl` ping RTT | median 0.20 ms |
| A small GET (SW + tunnel) | median 1.2–1.4 ms |

**The signaling path does not affect the data path** — the expected
behaviour: signaling only builds the connection, and the data flows
directly over WebRTC. The same order of magnitude as the phase 1 loopback
values.

### The bundle size

| | gzip |
|---|---|
| Phase 1 (no Nostr) | 6.8 KB |
| Phase 2 (Nostr included) | **35.8 KB** |

+29 KB: the honest cost of Nostr crypto in the browser (secp256k1
schnorr, ChaCha20-Poly1305, HKDF, bech32). Verified from the source map:
the bundle holds only `@noble/hashes`, `@noble/curves`, `@noble/ciphers`,
`@scure/base` and `nostr-tools`; `@scure/bip32`/`bip39`, the wordlist and
`nostr-wasm` **did not get in** (thanks to the subpath imports).

### Not measured yet (honestly)

- **Two devices / two networks.** Phase 2's real acceptance criterion. It
  cannot be verified from one machine; `docs/MANUAL-TEST.md` → the "two
  devices" section. Because the NAT on this network is symmetric, TURN
  (phase 5) may be needed if the other side is symmetric too.
- **Firefox and Safari.** Still unmeasured; the e2e suite only runs
  Chromium.
- **Relay resilience.** A relay going down mid-session and reconnecting (a
  phase 4 subject).
- **Concurrent visitors.** The `--max-sessions` default is 4; it was not
  measured under real load.

### Reproducing the measurement

```bash
# Relay health and NAT
cd agent && go run . doctor

# With the fake relay (deterministic)
cd web && npx playwright test nostr.spec.ts 2>&1 | grep MEASURED

# With real relays
cd web && PEERPAGES_E2E_RELAYS="wss://nos.lol,wss://nostr.mom" npx playwright test nostr.spec.ts
```

---

## Phase 3 — 2026-10-26 (several targets and the visitor experience)

### The bundle size

| | gzip |
|---|---|
| Phase 1 (no Nostr) | 6.8 KB |
| Phase 2 (Nostr included) | 35.8 KB |
| Phase 3 (+ the QR encoder) | **39.7 KB** |

+3.9 KB: the hand-written QR encoder (byte mode, level M, versions 1–10).
A ready-made package (`qrcode`) would have come as a separate chunk at
about 15–20 KB gzip and meant a second runtime dependency.

### The data path did not change

| Measurement | Phase 2 | Phase 3 |
|---|---|---|
| URL to an open tunnel (fake relay) | 356 ms | 357 ms |
| URL to an open tunnel (real relays) | 979 ms | 962–987 ms |
| A 5 MB download (the Nostr path) | 88–93 ms | 86–106 ms |
| A 5 MB download (the manual path) | about 100 ms | 95 ms |
| `ctrl` ping RTT | 0.20 ms | 0.20 ms |
| A small GET (SW + tunnel) | 1.2–1.4 ms | median 1.25 ms, p95 1.90 ms |

The expected result: phase 3 did the target selection and the screen work
and never touched the data path. The liveness ping the agent sends every 10
seconds has no measurable effect.

### QR

| | Value |
|---|---|
| The visitor URL (npub plus a target) | 100 characters → version 5 |
| The canvas | 228 px (4 px per module plus a 4-module quiet zone) |
| The dark module ratio | 0.378–0.388 |

The dark ratio is checked in e2e: that also catches an empty or all-black
canvas.

### The agent's counters

`peerpages serve` prints a status line to stdout every 5 seconds, **only
when something changes**:

```
[status] sessions 1 | requests 42 (success 42, errors 0) | in 1.2 KB | out 5.0 MB
```

The outgoing bytes are the **body** only: the ctrl JSON and the frame
headers are not counted, so the real traffic on the wire is somewhat more.

### The idle check

| | Value |
|---|---|
| The liveness ping interval | 10 s |
| The idle limit | 30 s |
| How long the SW trusts a tunnel owner | 5 s (then it re-probes) |

The Go tests verify these durations with short injected values
(`agent/internal/tunnel/idle_test.go`); in e2e it is checked that the
agent releases the session when the tab closes.

### Not measured yet (honestly)

- **Two devices / two networks, and that the QR really scans.** It cannot
  be verified from one machine; `docs/MANUAL-TEST.md` → "Trying it with
  two devices".
- **A tab killed by the browser.** In the tests the tab closes properly,
  so the page sends a `bye`. The case where the 30-second check is the
  ONLY protection (a browser crash) has to be tried by hand.
- **Firefox and Safari.** Still unmeasured; the e2e suite only runs
  Chromium.
- **Concurrent load across targets.** Heavy traffic to two targets at once
  was not measured.

### Reproducing the measurement

```bash
# The phase 3 e2e suite (several targets, the screen, the QR, the idle check)
cd web && npx playwright test phase3.spec.ts 2>&1 | grep MEASURED

# The bundle size
cd web && MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/

# With real relays (pass ONE relay: starting agents back to back makes
# public relays refuse rapid repeat connections)
cd web && PEERPAGES_E2E_RELAYS="wss://nos.lol" npx playwright test nostr.spec.ts
```

---

## Phase 4 — 2026-10-27 (distribution and resilience)

### The reconnect time

The agent was killed and restarted **with the same key**; nothing was
clicked in the visitor tab (`web/e2e/phase4.spec.ts`).

| What | Time |
|---|---|
| The tunnel coming back after the agent restarts | **6.3 s** (6.26 / 6.33 s in two runs) |
| The first backoff step | 1 s |
| The agent saying "ready" (fake relay) | about 0.3 s |

Most of those 6.3 seconds is the second backoff step: the first attempt
happens before the agent starts listening and is wasted (ephemeral events
are not stored), and the second attempt lands. With real relays that time
grows by the relay latency (the phase 2 measurement: URL to tunnel about
1 s).

### A request during the drop

A `GET /api/items` from the visitor tab while the tunnel dropped
mid-request: it returned **200** (the previous behaviour: 502). The
Service Worker waits for the reconnect and retries the request once; the
waiting bound is 10 s for subresources and 3 s for navigations.

### Binaries for three platforms (goreleaser, a local snapshot build)

| Platform | Archive |
|---|---|
| linux/amd64 | 7.1 MB |
| linux/arm64 | 6.5 MB |
| darwin/amd64 | 7.3 MB |
| darwin/arm64 | 6.8 MB |
| windows/amd64 | 7.3 MB |
| windows/arm64 | 6.5 MB |

Six archives plus `checksums.txt`. The build time (six targets, a cold
cache): **1 min 32 s**; 11 s with a warm cache. `-trimpath`,
`CGO_ENABLED=0`, `-s -w`.

### The bundle size

| | gzip |
|---|---|
| Phase 3 | 39.7 KB |
| Phase 4 (+ the reconnect supervisor) | **40.7 KB** |

+1.0 KB: `src/reconnect.ts` and the new rows on the status screen.

### The data path did not change

Ping RTT median 0.30 ms, a small GET median 1.50 ms / p95 2.50 ms, a 5 MB
download 260 ms — inside the phase 1–3 ranges. The new checks (the
liveness ping, the relay health poll) have no measurable effect.

### Not measured yet (honestly)

- **A real sleep/wake.** The laptop lid was not closed and opened; the
  automated tests produce the same result by restarting the agent, but it
  is not the same thing. The method: `docs/MANUAL-TEST.md` →
  "Sleep/wake (phase 4)".
- **The release workflow running on GitHub Actions.** Nothing was pushed
  and nothing was tagged. Locally `goreleaser check` plus `--snapshot`
  were verified; the workflow itself **was not run**.
- **Long-term relay resilience.** How many times an agent left open for
  hours loses its relay connections was not measured.
- **Firefox and Safari.** Still unmeasured (the BRIEF section 7 manual
  matrix).

### Reproducing the measurement

```bash
# The phase 4 e2e suite (the reconnect, a relay going down, the SW waiting window)
cd web && npx playwright test phase4.spec.ts 2>&1 | grep MEASURED

# Binaries for three platforms (it DOES NOT PUBLISH)
goreleaser check && goreleaser release --snapshot --clean
ls -la dist/
```

---

## Phase 5 — 2026-10-27 (the TURN fallback)

### What TURN costs: compared with the direct path

On one machine, with the fake TURN server (`peerpages faketurn`), from the
same test site. `relayOnly=true` forces the agent to gather relay
candidates only, so the direct path is **impossible**.

| | The chosen route | Ping RTT (median) | A 5 MB download | A small GET (median / p95) |
|---|---|---|---|---|
| Direct | `prflx` | 0.30 ms | **96 ms** (52.2 MB/s) | 1.70 / 2.20 ms |
| Over TURN | `relay` | 0.30 ms | **114 ms** (43.8 MB/s) | 1.60 / 2.10 ms |

**How to read it:** over loopback TURN's extra hop cost about 16% (18 ms)
on 5 MB, while the ping and the small requests stayed inside the
measurement noise. On a real network the cost is larger: every packet
travels to wherever the TURN server is and back, so one round trip of that
distance is added to the latency. The quota is real too: a 5 MB download
takes 5 MB of the provider's quota.

### The time to build a tunnel over TURN

| What | Time |
|---|---|
| URL to an open tunnel (TURN only, fake relay) | **306 ms** |
| The same path on the Go side (no browser) | 3.1 s |
| A TURN allocation (`doctor`) | 52–57 ms |

The Go test taking longer is not ICE but setup: the 500 ms waited for the
fake relay subscription to settle, plus pion gathering candidates.

### ICE prefers the direct path

Run with TURN given but without `--ice-relay-only`, the chosen route came
out `prflx` (direct). So defining TURN does not waste the quota; the relay
is only used when there is no other way.

### NAT / STUN (unchanged)

On this network `doctor` still sees **symmetric NAT** (two STUN servers see
different external ports: the phase 2 measurement). Phase 5's advice line
now joins that with the TURN state in one sentence: "The NAT is symmetric
but TURN is ready".

### The bundle size

| | gzip |
|---|---|
| Phase 3 | 39.7 KB |
| Phase 4 | 40.7 KB |
| Phase 5 (+ the ICE configuration and the route indicator) | **41.6 KB** |

+0.9 KB: `src/ice.ts` (the STUN list, URL parsing, reading `getStats`) and
the new row in `#metrics`.

### Not measured yet (honestly)

- **A real TURN server.** Every measurement used the in-process fake
  server; latency and throughput over a real coturn or Cloudflare TURN
  were not measured.
- **Cloudflare Realtime TURN.** It was never run against a real account
  (`docs/TURN.md`).
- **Two networks (a hotspot ↔ Wi-Fi).** Phase 5's real acceptance
  attempt; it cannot be done from one machine, and the method is in
  `docs/MANUAL-TEST.md`.
- **The `turns:` (TLS) path.** It can be given to the agent but was not
  measured; `doctor`'s allocation attempt skips TLS and tries UDP.

### Reproducing the measurement

```bash
# The phase 5 e2e suite (a tunnel over TURN, the negative control, the doctor report)
cd web && npx playwright test phase5.spec.ts 2>&1 | grep MEASURED

# The Go side (no browser): relay-only plus faketurn
cd agent && go test ./internal/connect/ -run TURN -v

# The doctor TURN check (against the fake server)
cd agent && go run . faketurn --port 0 --user u --pass p   # terminal 1
cd agent && go run . doctor --turn turn:127.0.0.1:PORT --turn-user u --turn-pass p
```

## Phase 6 — 2026-10-27 (the WebSocket and SSE bridge)

The measurements are on one machine (Windows 11, Chromium, loopback): the
visitor tab, the tunnel-owner tab and the agent are all on the same
computer.

### The WebSocket bridge

| What | Value |
|---|---|
| A small message round trip (median, n=20) | **0.40 ms** |
| A 1 MB message round trip (so 2 MB of tunnel traffic) | **40 ms** |
| Text plus binary plus a 40 KB message plus a close round | 10 ms |

How to read it: a small ws message's round trip is the same order as the
`ctrl` ping RTT (0.25 ms, phase 1) — so the bridge's own cost sits inside
the measurement noise. The path: visitor tab → `MessagePort` →
tunnel-owner tab → data channel → agent → the local ws server and back. A
1 MB message is split into 64 frames in each direction (16 KiB minus the
1 flag byte).

### Vite HMR (the acceptance criterion)

| What | Value |
|---|---|
| From a source file changing to the page updating | **38 ms** |
| Did the page reload | **no** (the window marker is still there) |

No HMR setting was given: Vite's client derives the ws address from the
page's own origin and the shim takes that address into the tunnel. The
subprotocol (`vite-hmr`) and the token pass through as they are.

### SSE incremental delivery

| What | Value |
|---|---|
| The first event | **2 ms** |
| The third event (the server waits 400 ms) | 804 ms |

This is what the phase 1 SSE test **could not measure**: that test awaited
the whole body with `res.text()`, so it would have passed even if all
three events arrived in one frame. The first event arriving 800 ms before
the third shows the stream really is incremental.

### The timeout fix

Until phase 6 `internal/proxy` wrapped the request context in a 60-second
deadline and that covered **the whole response**: every stream living
longer than 60 seconds was cut off silently. The bound now covers the
header phase only. The Go tests pin that down quickly (with a 300 ms bound
and an upstream that stays quiet for four times as long):

| Test | What it proves |
|---|---|
| `TestServeBodyHasNoDeadline` | There is no deadline while the body streams |
| `TestServeHeaderPhaseStillTimesOut` | A target that never answers cannot hold a request (0.30 s) |
| `TestServeStreamStopsOnCancel` | The stream stops right away when the visitor gives up |

### The bundle size

| | gzip |
|---|---|
| Phase 4 | 40.7 KB |
| Phase 5 | 41.6 KB |
| Phase 6 (+ the socket API, the injector, the bridge types) | **43.2 KB** |
| `ws-shim.js` (a separate file, loaded only when the flag is on) | **1.9 KB** |
| `sw.js` | 2.7 KB |

The shim is a separate file and does not enter the app shell's bundle:
with the flag off the visitor never downloads it.

### Not measured yet (honestly)

- **Socket.IO.** Not tested automatically (two new dependencies); the
  manual verification is in `docs/MANUAL-TEST.md`. So the claim "Socket.IO
  works" has not been measured.
- **ws latency on a real network.** Every ws measurement is loopback; on
  different networks a message round trip goes over the data channel on
  top of the connection's own round trip.
- **Long-term socket resilience.** The behaviour of a socket left open for
  minutes or hours (sleep/wake, a relay going down) was not measured; if
  the tunnel drops, the sockets go down with 1006 and the reconnect is
  left to the page's own library.
- **Firefox/Safari.** The shim and the `TransformStream` injection only
  ran in Chromium (the e2e matrix is Chromium).

### Reproducing the measurement

```bash
# The phase 6 e2e suite (the ws echo plus its measurements, SSE, Vite HMR)
cd web && npx playwright test phase6.spec.ts 2>&1 | grep MEASURED

# The timeout behaviour (Go, no browser)
cd agent && go test ./internal/proxy/ -run "Stream|Deadline|HeaderPhase" -v

# The ws bridge (Go, pion loopback)
cd agent && go test ./internal/tunnel/ -run TestWS -v

# The bundle size
cd web && node scripts/build.mjs /PeerPages/
```

## Phase 7 — 2026-10-27 (publishing and the launch)

This phase has **no new performance measurement**: the code path did not
change, only the `DefaultAppURL` constant and the documents did. The two
numbers worth recording:

| What | Value |
|---|---|
| The screenshots (`docs/img/`, from a real tunnel) | 52 KB (36 + 16 KB) |
| The bundle (unchanged) | 43.2 KB gzip plus the separate 1.9 KB shim |

### The acceptance criterion — measured LIVE

The site is up: <https://aliosmanmho.github.io/PeerPages/>. The
measurement was taken on the real chain — the page on GitHub Pages,
**real** public Nostr relays (3/4 connected; `relay.damus.io` answered
503) and the local agent.

| What | Value |
|---|---|
| The Pages build (the branch-based publish) | **27 s** |
| From the page refresh to an open tunnel | **8.6 s** |
| A small request (median, n=10) | **2 ms** |
| A 5 MB download | **114 ms** (44 MB/s) |
| The chosen ICE route | `host` (both ends on one machine) |

How to read it: **8.6 seconds** is twenty times the 300 ms of the
fake-relay e2e run, and the whole difference is in signaling — opening
WebSockets to real relays, publishing the event, and the agent seeing it.
Once the tunnel is up the data path is as fast as it is locally (2 ms /
44 MB/s), because the traffic already goes directly between the two
machines; GitHub Pages only served the page.

What the chain verified: the agent was run **with no flags**, the URL it
printed (`DefaultAppURL`) was pasted into the browser as it was, the
connect screen came up, and the local site appeared at
`https://aliosmanmho.github.io/PeerPages/` — with its CSS, its image and
the API list the JS filled in.

### What could not be measured: Actions

`main` was pushed but **the Pages and CI workflows could not start**:
there is a billing lock on the account (*"The job was not started because
your account is locked due to a billing issue"*). So the site was
published through the **branch-based** Pages path (the `gh-pages` branch
and GitHub's own Pages builder — it does not use Actions and does not hit
the lock).

Still unverified: `.github/workflows/pages.yml` and `ci.yml` have never
run on a GitHub runner, so `-race` and the CI version of `golangci-lint`
have only been tried locally (BRIEF section 8).

## Phase 8 — 2026-10-28 (an English project plus a multilingual page)

This phase has **no new performance measurement** either: nothing on the
data path changed. What was measured is the dictionary's cost and that the
test suites stayed green.

| What | Value |
|---|---|
| Phase 7 bundle | 43.2 KB gzip |
| Phase 8 bundle (+ the i18n dictionary, two languages) | **46.8 KB gzip** |
| Unit tests (web) | 412 |
| E2E tests (Playwright) | 39 (35 + 4 new i18n tests) |
| Go packages | the whole suite green, `-race` locally |

+3.6 KB for the two dictionaries together (about 110 keys each) plus the
language picker. The dictionary is part of the app shell bundle: it is not
a separate request, and it is not lazily loaded either, because a language
switch has to redraw the screen synchronously. `sw.js` stayed 2.71 KB and
`ws-shim.js` 1.88 KB — neither of them carries the dictionary.

### Not measured (honestly)

- **The bundle cost per language.** Both dictionaries are always in the
  bundle; splitting them per language was not tried, because at this size
  it would buy a round trip rather than save one.
- **The CI run.** The billing lock from phase 7 is still there, so the
  numbers above are local.

### Reproducing the measurement

```bash
cd web && npm test                       # the unit tests
cd web && npx playwright test            # the whole e2e suite
cd web && npx playwright test i18n       # the language tests only
cd agent && go test ./... -race          # the Go suite
cd web && node scripts/build.mjs /PeerPages/   # the bundle size
```
