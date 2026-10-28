# Changelog

The format of this file is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project is not
versioned yet; entries are kept per phase.

## [Unreleased]

### After phase 8 - the pre-launch checklist

Working through `docs/LAUNCH.md` section 6 on a clean `GOPATH` turned up
two things that would have greeted the first visitor:

- **The binary installed under the wrong name.** `go install
  .../PeerPages/agent@latest` names the binary after the last element of
  the module path, so it landed as `agent` while every document and every
  release archive calls it `peerpages`: the README quickstart's second
  command, `peerpages serve --target …`, failed for anyone who followed
  it. New `agent/cmd/peerpages` forwards to the same `cli.Execute`, and
  the documented command is now
  `go install github.com/aliosmanmho/PeerPages/agent/cmd/peerpages@latest`.
  The module root stays a main package, so the old path still works — it
  just installs as `agent`.
- **The security reporting channel did not exist.** Both SECURITY files
  name GitHub Security Advisories as the preferred channel, but private
  vulnerability reporting was off on the repository. It is on now.

Also checked and found good: the live site, the printed visitor URL
(`DefaultAppURL`), every README command, every documentation link (five
external, all 200), and the repository description, homepage and topics.
The README images were retaken with the English interface, and the sample
API items became `alpha`/`beta`/`gamma`.

`v0.1.0` was then tagged and pushed, and the release built: six archives
plus `checksums.txt`, opened as a **draft** for review. The generated
changelog was turned off in `.goreleaser.yaml` — the commit history is
Turkish up to phase 8 and is not being rewritten (decision 3 below), so a
generated list would have put Turkish subjects on an English release page.
The notes are written by hand instead.

Still open: the demo GIF (a real screen recording, yours to make) and
publishing the draft release.

### Phase 8 - An English project plus a multilingual page

The repository is English from end to end, and the web page speaks both
English and Turkish with English as the default. The protocol did not change:
not one wire string, flag name or environment variable moved.

#### Four decisions

1. **The documents are English only**, with no Turkish copy kept — one source
   of truth rather than two that drift apart.
2. **Multiple languages on the web page only.** The agent's CLI output, its
   error messages and its logs stay **English only**: a translated log breaks
   both searchability and debugging.
3. **The commit history is left as it is**; messages from here on are
   English. No force push — changing 57 SHAs would kill the `gh-pages`
   history and every link already handed out.
4. **A branch plus a PR** (`english-i18n`), committed step by step.

#### GitHub

- Repository description, homepage and twelve topics; squash-only merges,
  `delete_branch_on_merge`, auto-merge, the wiki and projects tabs off.
- Community files, all English: `.github/ISSUE_TEMPLATE/*.yml`,
  `PULL_REQUEST_TEMPLATE.md`, `CODE_OF_CONDUCT.md` (Contributor Covenant
  2.1), and a three-line `.github/SECURITY.md` that points at
  `docs/SECURITY.md` so GitHub recognises the policy.
- MIT was already in place and recognised; it was not touched.

#### Web i18n

- New `web/src/i18n.ts`: no dependency, `t(key, params)` with `{placeholder}`
  substitution, `data-i18n` / `data-i18n-attr` applied to the DOM.
- Resolution order: `?lang=` → `localStorage["peerpages-lang"]` →
  `navigator.languages` → `en`.
- An unknown `?lang=xx` **does not throw**: it falls back to the default and
  writes to the console. This is a deliberate departure from the "invalid
  input throws" rule of `relaysFromSearch` / `stunFromSearch` — a missing
  relay list stops the tunnel from being built, a missing language is merely
  cosmetic, so dropping the page onto an error screen would be out of
  proportion. The reasoning is at the top of the file.
- A small `<select>` in the header; changing it redraws the whole screen,
  the metrics and the last status message.
- Module errors (`TunnelError`, `PeerIDError`, `SignalingError`, …) do NOT
  go through the dictionary: they are English and shown as they are. The
  ~124 strings in the dictionary are the screen's own text.
- The **Service Worker stays English**: its four plain-text bodies live in a
  separate build target with no DOM and no knowledge of the page's chosen
  language. An honest limit, written down in BRIEF section 8.

#### The code

- Every comment, doc comment and runtime string in `agent/internal/**` (81
  files) and `web/src/**` (53 files) is English — package doc comments
  included, since that is what `go doc` prints.
- The `slog` keys were deduplicated: `neden`/`reason` and `hedef`/`target`
  used to sit side by side; everything is one English form now (`limit`,
  `bytes`, `duration`, `silence`, `file`, `frames`, `failed`, `total`,
  `tick_interval`, `secret_required`, `first_ok`, `reported_bytes`).
- The stdout marker `---PEERPAGES-OFFER-BEKLENIYOR---` became
  `---PEERPAGES-OFFER-WAITING---`, with `web/e2e/fixtures/agent.ts` and
  `docs/MANUAL-TEST.md` changed in the same commit (otherwise e2e hangs
  waiting on stdout).
- Test fixtures: the `agent/testdata/site` ids (`title`, `items`,
  `description`, `download-status`), its dataset flags (`data-js-ran`,
  `data-api-ran`), the JSON field names, and `web/e2e/fixtures/vite-app`
  (`message.js`, `FIRST-VERSION`) — together with all 24 selector uses
  across six spec files.
- `faz3..6.spec.ts` → `phase3..6.spec.ts`, and the references in the docs.
- The ~200 `name` labels in the three test-vector files are English. These
  are labels only, never looked up, so the data and the structure are
  untouched and both languages still read the same file.
- CI step names, the `Makefile`'s `##` help lines, `.goreleaser.yaml` (the
  published Homebrew and Scoop `description` fields included), and the six
  `scripts/*.ps1` files.

#### The documents

All 3,770 lines of the twelve Turkish documents were **rewritten** in
English rather than translated line by line. Section numbers, table
structure, code blocks and file references were kept exactly as they were,
because the code holds 96 references of the `BRIEF section 8` kind.

Two stale claims were corrected rather than translated faithfully:
`protocol/README.md` still said the agent opens the data channels (untrue
since the phase 2 role swap), and `docs/LAUNCH.md`'s Show HN draft still
listed "the CLI and the detailed docs are still in Turkish" as an honest
limit.

The Turkish prompt in BRIEF section 10 is deliberately kept as a historical
record, with a note saying so.

#### The guard

New `scripts/check-language.mjs`: a list of ASCII-ised Turkish words
(`govde`, `cerceve`, `gecersiz`, `baglanti`, `oturum`, …) plus a
word-bounded regex for the diacritics. `node_modules`, `dist`, `.git`, the
Turkish dictionary with its tests, and the historical prompt in BRIEF
section 10 are excluded; a single line can opt out with a `lang-ok` marker.
A non-zero count exits 1. Adding it to CI waits for the Actions lock to be
lifted.

#### Tests

412 TS unit tests (+15), 19 Go packages, 39 Playwright tests (+4: English
by default, `?lang=tr` switches the whole screen, an unknown `?lang=` falls
back without an error, the picker survives a reload).

MEASURED: bundle 43.2 → 46.8 KB gzip (+3.6 KB for both dictionaries and the
picker); `sw.js` 2.71 KB and `ws-shim.js` 1.88 KB unchanged.

#### Acceptance status

**Met, and verified LIVE.** The branch was squash-merged
(github.com/aliosmanmho/PeerPages/pull/1) and the app shell republished; on
https://aliosmanmho.github.io/PeerPages/connect/ a first-time visitor gets
`<html lang="en">` and "Serverless tunnel", `?lang=tr` turns the whole
screen Turkish and the choice is remembered, and `?lang=zz` falls back to
English with a console warning instead of an error. The guard script reports
zero leftover Turkish.

GitHub Actions is still billing-locked, so CI did not run on the PR (the
test results above were produced locally) and publishing again went through
the `gh-pages` branch rather than pages.yml (26 s).

### Phase 0 — The skeleton

- The monorepo skeleton: `web/`, `agent/`, `protocol/`, `docs/`, `scripts/`,
  `.github/workflows/`.
- The MIT licence, a draft `CONTRIBUTING.md`, this changelog.
- `web/`: Vite + TypeScript, no UI framework, no runtime dependency. The
  Service Worker is built **separately** (`vite.sw.config.ts` → an unhashed
  `dist/sw.js`); `vite-plugin-pwa` was not used. `base` can be set with
  `PEERPAGES_BASE` for a GitHub Pages subpath. Three separate `tsconfig`
  files (app / sw / node).
- `agent/`: Go + cobra. `peerpages serve --target ...`; `--max-body` (50MB),
  `--max-concurrent` (64), `--manual`, `--stun`, `--verbose`.
  - `internal/target`: the target allowlist; deriving a host from the request
    path is impossible (SSRF protection, BRIEF principle 1).
  - `internal/logging`: URL paths are masked by default (principle 6).
  - `internal/size`: size expressions (base 1024).
- Lint and format: `biome` (web), `gofmt` + `go vet` + `golangci-lint`
  (agent).
- A `Makefile` plus `scripts/*.ps1` equivalents for Windows, `docs/DEV.md`.
- `.github/workflows/ci.yml`: web lint+test, agent lint+test (-race), e2e.

**Acceptance (verified):** `npm run dev` → `http://localhost:5173` returns
200; `peerpages --help`, `peerpages serve --help` and `peerpages --version`
work.

### Phase 1 - The core proof: manual signaling plus the SW proxy

No signaling server and no Nostr: the offer and the answer are carried by
copy-paste. The point is to prove the SW + WebRTC + reverse proxy chain
(BRIEF phase 1).

#### The protocol (`protocol/`)

- `protocol/README.md` is the normative definition and
  `protocol/testvectors.json` its machine-readable counterpart. **Both the TS
  and the Go tests read the same file.**
- The `data` frame: `[4B id BE][4B seq BE][payload <= 16384]`.
- `ctrl`: `req`/`res`/`end`/`abort`/`ping`/`pong`/`hello`; the error codes and
  the error order are defined.
- Canonical serialization: the two implementations produce byte-identical
  output, and the vectors prove it.
- **The `end` message carries `frames` and `len` counters** (the decision
  below).

#### The agent (`agent/`)

- `internal/signal`: base64(JSON) SDP transport for manual signaling.
- `internal/tunnel`: a pion/webrtc v4 session, two data channels, hello,
  ping/pong, request multiplexing, backpressure (1 MiB / 256 KiB thresholds),
  concurrency and body-size ceilings.
- `internal/proxy`: the reverse proxy. Hop-by-hop header stripping, `Host`
  rewriting, `Location`/`Content-Location` turned into relative paths, header
  injection refused, the body streamed (the full body is never buffered),
  redirects not followed, no error text leaked.
- `internal/testsite` + `testdata/site`: HTML/CSS/JS/PNG/JSON/302/SSE and a
  deterministic 5 MB `big.bin` (generated, not kept in the repo).
- `peerpages serve` really opens a tunnel now; the hidden `peerpages testsite`
  command provides a local server for the tests.

#### The web app (`web/`)

- `src/sw.ts`: a hand-written Service Worker. It catches the requests in its
  scope, forwards them to the tunnel page over a `MessageChannel` and streams
  the response back with a `ReadableStream`. The scope is derived **at
  runtime** from `registration.scope`.
- `src/tunnel.ts`: the page-side multiplexer; the counter rule, seq
  validation, backpressure, AbortSignal, timeouts.
- `src/signal.ts`, `src/bridge.ts`, `src/page-bridge.ts`, `src/boot.ts`.
- The connect screen: paste the offer → produce an answer → open the tunnel,
  with status and counters.

#### Changed decisions (both found by measurement, both asked about)

1. **There is no ordering guarantee between `ctrl` and `data`.** Measured: in
   a 90 KB response all six body frames arrived AFTER `end`. The fix: `end`
   gained `frames`/`len` counters, and the receiver waits until the counters
   are complete. In the request direction, frames arriving before `req` are
   buffered. The two channels, the 16 KiB framing and the "`end` on ctrl"
   decisions were kept.
2. **The app shell and the local site's root both wanted the same URL.** The
   fix: the connect screen and the tunnel-owner page live at `/connect/`, and
   the scope root is the local site's root. The tunnel survives even if the
   owning tab is refreshed. The `#peer-id` URL shape was kept.

#### Tests

- 128 TS unit tests, 8 Go packages (framing, ctrl, the target
  allowlist/SSRF, header stripping, Location rewriting, sizes, log masking,
  tunnel ordering regressions).
- An end-to-end test of the agent half over a real pion loopback connection.
- **Playwright e2e (Chromium, 8 tests): every phase 1 acceptance criterion
  green.** HTML/CSS/JS/image/JSON, the 5 MB file (sha256), falling back to the
  connect screen and a 503 with no tunnel, Location rewriting, SSE, SSRF.

#### Measurements

Loopback, Chromium: a 5 MB download ~103 ms (~48 MB/s), ctrl ping RTT median
0.2-0.3 ms, a small GET median ~1.2 ms / p95 ~2.3 ms. The agent half alone
carries 5 MB in 63 ms (~79 MB/s); the difference is the cost of the SW
bridge. The detail is in `docs/MEASUREMENTS.md`.

#### Documentation

- `docs/MANUAL-TEST.md`: the two-window and two-device scenarios, a
  checklist, troubleshooting.
- `docs/MEASUREMENTS.md`: what was measured and what was **not**
  (Firefox/Safari, a real network, concurrent load).
- BRIEF section 8: two decisions closed, Firefox/Safari left open.

### Phase 2 - Nostr signaling

Copy-paste is gone: the visitor only opens the URL. Signaling goes over
public Nostr relays; there is still no server, and the relays cannot see the
content (NIP-44).

#### Changed README decisions (both asked about and approved)

1. **peer-id = the FULL npub** (shortened on screen). The README said "a
   short form of the npub", but NIP-44 and the `#p` tag want the full 32-byte
   pubkey, and that cannot be derived from a shortened id.
2. **The VISITOR publishes the offer** and the agent returns the answer. The
   agent cannot send the first message because it does not know the visitor's
   ephemeral pubkey in advance. With this direction the message list stayed
   valid as it was, and the connection is built in 2 legs instead of 3. The
   visitor opens the channels too; Session is now answerer-only (a single
   code path, manual signaling included).

#### The protocol

- `protocol/signaling.md` is the normative definition and
  `signaling-testvectors.json` its machine-readable counterpart. The vectors
  were produced by a THIRD independent implementation
  (`gen-signaling-vectors.py`); both Go and TS verify against it.
- The messages: offer / answer / candidate / bye. `sid` (16 hex) is the
  session id.
- The `--secret` layer sits UNDER NIP-44: HKDF-SHA256 + ChaCha20-Poly1305,
  a payload with the `pps1.` prefix. All three implementations produce a
  byte-identical payload from the same key and the same nonce.

#### The agent

- `internal/nostr`: the relay pool (hedged publishing, connecting ahead of
  time), NIP-44.
- `internal/nostr/fakerelay`: an in-process test relay; dropping ephemerals,
  asking for auth, refusal scenarios.
- `internal/doctor` + `peerpages doctor`: NIP-11, connection and an
  **ephemeral echo** test; a NAT-type guess from two STUN servers.
- `internal/identity` + `peerpages key`: the keypair, `~/.peerpages/key`
  (nsec), the peer-id. An existing key is NEVER overwritten.
- `internal/connect`: the stream that turns offers into sessions, plus the
  visitor rules (replay, clock skew, rate limit, session ceiling).
- `serve` defaults to Nostr; `--manual` is the manual path. New flags:
  `--relay`, `--secret`, `--key`, `--app-url`, `--max-sessions`.

#### The web app

- `src/nostr/`: peer-id parsing, the relay pool, the visitor flow, the relay
  list (`?relay=`).
- The connect screen: with a peer-id in the URL it connects on its own, and
  there are error screens ("the agent did not answer", "the secret does not
  match", "the data channels did not open").

#### Measured and found

- The relay list was verified live; `relay.nostr.band` was dropped from the
  default because it is unreachable from this network, and primal plus
  nostr.mom took its place.
- **This network's NAT is symmetric** (the two STUN servers saw different
  external ports): if the other side is symmetric too, TURN is needed
  (phase 5). `doctor` says so up front.
- **A bug found while running against real relays:** the agent said "ready"
  before the subscription was established, and because ephemeral events are
  not stored, an offer arriving right away was lost. The order was fixed:
  connect → subscribe → say ready.
- From the URL to an open tunnel: 356 ms (a fake relay), **979 ms (real
  relays)**.
- Bundle 6.8 KB → 35.8 KB gzip; bip32, bip39 and wasm stayed out.

#### Tests

216 TS unit tests, 13 Go packages (the connect flow is tested over a fake
relay with real NIP-44 and real WebRTC), 16 Playwright tests (8 manual path +
8 Nostr path). Nostr e2e against real relays 7/7.

#### Acceptance status

Phase 2's "two devices on different networks" criterion is **waiting on the
user**: it cannot be tested from a single machine. The method is in
`docs/MANUAL-TEST.md`.

### Phase 3 - Several targets and the visitor experience

One agent publishes several local projects, and the connect screen shows
meaningful information now.

#### Several targets

- The target name is carried in the URL: `#npub1…/admin`. The fragment
  grammar is `npub [ "/" target ] [ ":" secret ]`; the '/' is only looked for
  BEFORE the ':', because the secret is free text and may contain a '/'.
- The target travels in the signaling `offer` (`protocol/signaling.md`); the
  data channel protocol and phase 1's canonical key order did not change. One
  target per session.
- An unknown target is refused with `bye`/`unknown-target` and the session is
  NEVER established. There is no silent fallback to `default`.
- The agent prints a separate visitor URL for each target. On the manual
  signaling path `--manual-target` picks the target.

#### The connect screen

- Latency is measured CONTINUOUSLY (a ping every 5 s): the last value plus
  the median plus n.
- Requests/errors, bytes received and sent, the instantaneous rate, requests
  in flight, the target list (from the ctrl `hello`), the active target.
- A "copy the address" button (with an execCommand fallback for insecure
  contexts — there was no clipboard API in the two-device trial) and the QR
  panel.

#### The QR encoder

- `web/src/ui/qr.ts`: hand-written, no dependency. Byte mode, level M,
  versions 1-10. It does not draw: it returns a matrix, so it can be tested
  without a DOM.
- The vectors were produced by a THIRD independent implementation (python
  `qrcode`). 15 mask-forced vectors byte for byte, 5 vectors for automatic
  mask selection. The vectors caught two bugs (duplicated format information,
  a column shift in data placement).
- Bundle 35.8 → 39.7 KB gzip.

#### Counters

- `agent/internal/stats`: requests/successes/errors and bytes per target,
  atomic.
- A 404 or 500 from the target counts as SUCCESS: the proxy did its job. The
  error counter is only for the cases where the agent could not carry the
  response.
- A status line on stdout every 5 s, only when something changed.

#### Releasing resources

- The agent sends a ctrl `ping` every 10 s and closes a session that has not
  answered for 30 s. The protocol did not change: the page already returned a
  pong to an incoming ping.
- The SW re-checks a stale tunnel owner (`clients.get` kept returning a
  client as long as the tab was alive).
- The detection on the SW side is LAZY: with no request arriving there is no
  check either. The real time-bounded protection is the audit in the agent.

#### A bug found on the way

A body frame over the ceiling arriving BEFORE `req` was dropped silently:
because the body never arrived, the proxy waited forever, the visitor got no
abort, and the request hung — the ceiling protection turned into a resource
leak. A flaky test (3 red out of 6 runs) exposed it; the problem was not the
test but the behaviour itself.

#### Tests

276 TS unit tests, 15 Go packages, 22 Playwright tests (8 manual path +
8 Nostr + 6 for phase 3). Nostr e2e against real relays (a single relay) 7/7.

#### Acceptance status

Phase 3's criterion (one agent publishes two projects, the screen shows
meaningful status) was met by the automated tests. Reading the QR with a real
phone, and the two-device/two-network verification, are on the user's side:
`docs/MANUAL-TEST.md`.

### Phase 4 - Distribution and resilience

A broken connection picks itself up now, and the agent became installable.

#### Resilience (the agent)

- The idle audit runs on the MONOTONIC clock. It used to store `lastSeen` as
  unix nanos and read it back with `time.Unix`; a `time.Time` built that way
  carries no monotonic reading, so the comparison was wall-clock. The time
  spent suspended counted as "silence" as it was, and the FIRST TICK after
  waking closed every session.
- The audit also looks at its own lateness: if more than 3× the expected gap
  passed between two ticks, that is a wake-up, the silence counter is reset
  and the session is tried with one more ping. The decision to close always
  rests on a ping that went unanswered.
- ICE `Disconnected` is no longer terminal: a 10-second tolerance
  (`DisconnectGrace`); if it comes back the session lives, otherwise
  `ErrDisconnected`. It used to be closed instantly and reported as a CLEAN
  close.
- The close reason is in the log: `idle` / `disconnected` / `clean`.
- `internal/nostr`: per-relay health (`Health`, `HealthyCount`).
- `Acceptor.Run` is an audited loop: it resubscribes when the subscription
  closes, when no relay is connected (3 checks) or when the process appears to
  have been suspended; the filter's `since` is refreshed every period. The
  backoff is 1→60 s. Previously Run returned an error when the subscription
  closed and the agent died with `exit 1`.
- A `relay 3/4` field in the status line.

#### Resilience (the web app)

- New `src/reconnect.ts`: a controller with backoff (1-2-4-8-15-30 s). Every
  attempt builds a session FROM SCRATCH (a new sid plus a new ephemeral
  keypair) — this path was chosen over an ICE restart because the agent is
  answerer-only and a session is single-use (the departure note in BRIEF
  section 8).
- `online` and `visibilitychange` reset the backoff (the wake-up path).
- Liveness check: two unanswered pings in a row count as a disconnect. The
  WebRTC state machine does not report every break.
- The manual signaling panel does NOT open on a disconnect; the screen shows
  the countdown, the counters are kept, and `#metrics` holds the reconnect
  count.
- The SW holds requests during the gap: subresources for 10 s, navigations
  for 3 s. A GET/HEAD broken mid-request is retried once (only if no response
  header ever went out: errors coming from the target are not retried).

#### Distribution

- `internal/version`: ldflags (-X) with a `runtime/debug.ReadBuildInfo`
  fallback. A binary installed with `go install ...@latest` knows its version
  too.
- A new `peerpages version` command (version, commit, date, platform, Go).
- `.goreleaser.yaml`: linux/darwin/windows × amd64/arm64, CGO off,
  `-trimpath`, tar.gz (zip on Windows), `checksums.txt`. The release opens as
  a DRAFT. Scoop and Homebrew are drafted but upload is off (the tap
  repositories do not exist).
- `.github/workflows/release.yml`: goreleaser on a `v*` tag.
- `ci.yml`: an ubuntu/macos/windows build matrix.
- `docs/INSTALL.md`: downloading a binary, `go install`, building from
  source, verification, running as a service on Windows (Task Scheduler /
  NSSM — documentation only).
- `Makefile`: `release-snapshot`, `release-check`; `lint-agent`'s silent
  gofmt failure was fixed (it returned 0 even when it listed files).
- `go mod tidy`: pion, go-nostr and coder/websocket are direct dependencies.

#### A bug found on the way

The first version of the SW waiting window only kicked in when there was "no
tunnel-owner tab". If the tunnel broke mid-request the request was still
dispatched, failed on the page and the visitor saw a **502** — so the window
never did anything on that path. A new e2e test caught it.

#### Tests

285 TS unit tests (+9), 16 Go packages, 26 Playwright tests (+4: reconnect,
the disconnect screen, a relay going down, the SW waiting window).

MEASURED: after the agent restarted, the tunnel came back in 6.3 s; the six
platform archives are 6.5-7.3 MB; bundle 39.7 → 40.7 KB gzip.

#### Acceptance status

The reconnect criterion was met by the automated tests (the agent
restarting, a relay going down and coming back, the disconnect gap). REAL
sleep/wake has to be tried by hand: `docs/MANUAL-TEST.md` → "Sleep and wake
(phase 4)". The release workflow was verified LOCALLY (`goreleaser check` +
`--snapshot`: 6 archives plus checksums); it was not run on GitHub Actions,
because there is no push and no tag (BRIEF section 8).

### Phase 5 - The TURN fallback

A way out for networks that cannot connect (symmetric NAT). The protocol did
NOT change: no field was added to the signaling or the data channel messages.

#### Decision: TURN on the agent side only

Neither a TURN address nor a credential is sent to the visitor. The forcing
reason is structural: the visitor produces the offer BEFORE hearing a single
word from the agent, so sending TURN information through signaling would be
far too late. One-sided TURN is enough (the agent gathers a relay candidate
and the visitor sends packets to that public address) and the quota stays
with the owner of the secret.

#### Decision: no Cloudflare Worker was written (a departure from BRIEF)

The Worker's only job was to keep the API token out of THE BROWSER; since
only the agent uses TURN and the token already sits on the owner's machine,
a server in between is unnecessary. The agent fetches the credential from the
API itself. The "one piece that needs a server" disappeared completely.

#### The agent

- New `internal/ice`: ParseServerURL (stun/stuns/turn/turns, 3478/5349 for an
  address with no port, `?transport` preserved, user information inside the
  URL REFUSED), Config.Validated(), Servers() (STUN and TURN as separate
  entries: Username/Credential are per entry), Policy(), Describe() (the
  password is never printed), DefaultSTUN.
- `ICEServers` + `ICEPolicy` instead of `tunnel.Config.STUN`;
  `ICETransportPolicy` is used for the first time. `connect.Config.ICE` is a
  FUNCTION (ICEFactory): a Cloudflare credential has a lifetime, so every new
  session gets the current list.
- `serve`: --turn, --turn-user, --turn-pass (PEERPAGES_TURN_PASS),
  --ice-relay-only, --turn-cloudflare-key-id, --turn-cloudflare-token
  (PEERPAGES_CF_TURN_TOKEN). Validation at startup and an `ice` line in the
  banner.
- `internal/ice/cloudflare.go`: a short-lived credential, refreshed at 80% of
  the TTL, and if the refresh fails the old one keeps being used. If the
  first fetch fails the agent does NOT START (carrying on silently without
  TURN would be wrong).
- `doctor`: an ALLOCATION is requested from every TURN server (not "is it
  up" but "does it hand out a relay address"), and the relay address goes
  into the report; Report.Advice combines the NAT verdict with the TURN
  result; if TURN was asked for and none of them gave an allocation the exit
  code is non-zero. Symmetric NAT on its own is NOT an error.
- `internal/ice/faketurn` plus a hidden `peerpages faketurn` command
  (pion/turn v5, already an indirect dependency).

#### The web app

- New `src/ice.ts`: DEFAULT_STUN (the same as the Go side),
  normalizeStunURL, stunFromSearch (?stun=...), iceServers(),
  routeFromStats().
- Up to this phase the page gave NO ICE server at all (it only gathered host
  candidates); it now gives the default STUN list, and the visitor gathers
  its own srflx candidate too.
- A "connection path" line in `#metrics`: direct / over TURN (from the
  candidate pair chosen in `pc.getStats()`). The visitor sees that the
  traffic goes through a server; end-to-end encryption continues, but the
  path changes.

#### Learned on the way

The address a fake TURN server ANNOUNCES has to be the same as the source
address it sends packets from. With the relay bound to 127.0.0.1 the other
side could not reach it (pion and browsers do not produce a loopback
candidate), and binding to every interface while announcing 127.0.0.1 made
the browser treat the incoming check as peer-reflexive, so the selected
candidate pair DID NOT LOOK like a relay — the test could not measure a
working TURN. On a real TURN server that consistency is there already.

#### Tests

309 TS unit tests (+24), 18 Go packages, 30 Playwright tests (+4). On the Go
side the tunnel opens with relay-only plus faketurn, and the negative control
(no TURN) cannot connect.

MEASURED: a 5 MB download 96 ms directly / 114 ms over TURN (~16%); from the
URL to a TURN tunnel 306 ms; an allocation 52-57 ms; bundle 40.7 → 41.6 KB.

#### Acceptance status

The criterion (it connects over TURN in a symmetric-NAT simulation) was met
by the automated tests: `--ice-relay-only` makes the direct path impossible,
the tunnel opens and the screen says "over TURN". A REAL two-network trial
and a Cloudflare account are on the user's side (docs/MANUAL-TEST.md,
docs/TURN.md).

### Phase 6 - The WebSocket and SSE bridge

#### The protocol

- Three new ctrl messages: `ws-open` (id, path, protos), `ws-ok` (id, proto),
  `ws-close` (id, code, reason). There is NO `ws-frame`: the payload goes over
  the `data` channel.
- A ws frame subformat: the outer frame ([4B id][4B seq][payload]) was kept
  EXACTLY as it was, and the payload of a frame belonging to a ws id became
  [1B flag][chunk] (the flag: text/binary plus last-chunk). Backpressure,
  16 KiB chunking and seq checking came for free; binary data takes no base64
  inflation and bulk ws traffic does not clog the ctrl channel.
- ws sessions share the SAME id space as HTTP requests.
- The vectors come from one file for both languages: 7 valid + 16 invalid
  ctrl, 6 valid + 4 invalid ws frame vectors.

#### The agent

- New `internal/wsbridge`: a connection to the local target with
  coder/websocket (already a direct dependency). The target URL comes ONLY
  from the allowlist (principle 1). The `Origin` header goes out as the
  target's own origin, without which Vite and Socket.IO refuse the handshake.
- `internal/tunnel`: ws session dispatch, a two-way pump, the `WSDialer`
  interface (nil = the bridge is off → ws-close 1008). No method was ADDED to
  the `Proxy` interface (it is implemented in several places in the tests).
- New flags: `--inject-ws-shim` (the bridge plus the shim, OFF by default),
  `--max-ws-message` (1MB), `--max-streams` (8).
- The test site: `/sse?n=&delay=&keepalive=` (the defaults did not change)
  and a new `/ws` echo endpoint.

#### The web app

- `Tunnel.openSocket`: a message-based ws interface; chunking, FIN
  reassembly, seq checking and backpressure are inside.
- New `src/ws-shim.ts` → `dist/ws-shim.js` (a classic IIFE, a third vite
  configuration). It only tunnels ws addresses going to the SAME HOSTNAME,
  and it computes the scope prefix from `document.currentScript.src`.
- New `src/inject.ts`: a TransformStream that inserts a `<script>` into the
  HTML stream (the tag may be split across frames; after 8 KiB it gives up
  and writes at the head).
- The SW TRANSFERS the port to the tunnel-owner tab during the shim
  handshake, and everything after that is tab ↔ tab. The SW can be killed
  while idle, and a long-lived socket cannot depend on it.
- A conditional "WebSocket: N open" line in `#metrics`.

#### A bug found on the way

`internal/proxy` wrapped the request context in a 60-second deadline, and
that covered the WHOLE response: every SSE stream living longer than 60
seconds was cut silently — the visitor saw no error, only "the stream
ended". The limit is in the Transport now and only on the header phase. A
target that never answers still cannot hold the request open.

#### Decisions (a departure from BRIEF)

The Service Worker, not the agent, inserts the shim into the HTML; the agent
only adds the `x-peerpages-ws-shim` header. That way the protocol version is
not broken (`hello` is one-way and accepts no optional field) and
`internal/proxy` stays byte-for-byte transparent. The reasoning is in BRIEF
section 8, the detail in docs/WEBSOCKET.md.

#### Tests

397 TS unit tests (+88), 19 Go packages (new: `internal/wsbridge`), 35
Playwright tests (+5).

MEASURED: a small ws message RTT median 0.40 ms; a 1 MB round trip 40 ms; a
Vite HMR update 38 ms (without the page reloading); the first SSE event 2 ms
/ the third 804 ms; bundle 41.6 → 43.2 KB plus a separate 1.9 KB shim.

#### Acceptance status

The Vite HMR criterion was met by an automated test: `vite dev` runs in a
fixture project, a source file changes and the page updates WITHOUT
RELOADING. The Socket.IO example is in the manual verification
(docs/MANUAL-TEST.md): it was not automated because it would pull in two new
dependencies.

### Phase 7 - Publishing and the launch

#### The name

PeerPages was SETTLED. No tag had ever been pushed, so this was the cheapest
possible moment to change the module path; it was not changed, because the
repository name, the Pages URL, the module path, the binary name and the
scope prefix were consistent already. The "the name may change in phase 7"
notes were removed from .goreleaser.yaml, docs/INSTALL.md and docs/DEV.md.

#### The documents

- The Turkish brief plus the roadmap plus the section 8 decision log moved
  into **docs/BRIEF.md**; the section numbers were kept exactly. The 96
  references in the code (54 files) and the 30 in the documents were updated
  mechanically; the diff is symmetric and touches only comment and text
  lines.
- An **English public README** at the root (223 lines): the pitch, real
  screenshots, a self-contained quickstart, "Why not ngrok?", how it works,
  the six numbered security principles (the same numbers as BRIEF section 6),
  nine honest limits, a seven-question FAQ.
- New **docs/SECURITY.md**: a four-actor threat model (a relay watcher,
  peer-id guessing, a malicious visitor, SSRF), a guard table built from the
  real constants, seven deliberate trade-offs, an out-of-scope section and
  five copy-and-run commands that verify the claims.
- New **docs/LAUNCH.md**: a GIF recording recipe, a Show HN title plus first
  comment plus answers to the hard questions, r/selfhosted and r/webdev
  texts, a social media text, a ten-item checklist. None of it was posted.
- A "the Pages deploy" section in docs/DEV.md; the fact that the web build is
  three steps (the shim) was corrected.

#### Distribution

- New `.github/workflows/pages.yml`: it deploys the app shell to Pages when a
  change under `web/**` or `protocol/**` reaches main. `configure-pages`
  turns Pages on itself with `enablement: true`; the `PAGES_BASE` and
  `PAGES_CNAME` repository variables open the custom-domain path in one step.
- `internal/cli/key.go`: DefaultAppURL is a real address now. The previous
  template value ("<user>") let an agent running with no flags print a URL
  that DID NOT WORK.
- `main` was pushed FOR THE FIRST TIME (54 commits).

#### Images

`web/e2e/demo.spec.ts` produces two screenshots from a real tunnel (52 KB in
total); it is skipped in a normal run (grepInvert /@demo/), so the test count
stays at 35.

#### Acceptance status

**Met, and measured LIVE.** The site is up
(https://aliosmanmho.github.io/PeerPages/): the agent was run with no flags,
the URL it printed opened as it was, the tunnel was built in 8.6 seconds and
the local site appeared at the github.io address. With real Nostr relays (3/4
connected) a small request took 2 ms and a 5 MB download 114 ms.

GitHub Actions COULD NOT RUN because of a billing lock on the account ("your
account is locked due to a billing issue"), so publishing was done not with
pages.yml but with GitHub's own branch-based Pages builder (the `gh-pages`
branch, build_type legacy — it does not use Actions, 27 s). The workflow is
still there; once the lock is lifted the source can be switched to "GitHub
Actions" with a single command and the branch deleted.

Still open: the workflows have never run on a GitHub runner (-race and the CI
version of golangci-lint only ran locally), and because there is no tag there
is no binary download path — today `go install` is the only installation
route.
