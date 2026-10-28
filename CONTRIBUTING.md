# Contributing guide (draft)

Phases 0–7 of PeerPages are done. The user-facing entry points are the
English `README.md` at the root and `docs/SECURITY.md`; this file holds the
working rules inside the repo. The project's internal brief and decision
log: `docs/BRIEF.md`.

## Monorepo layout

| Directory | Contents |
|---|---|
| `web/` | The static app: Vite + TypeScript, no framework. The app shell plus a hand-written Service Worker. |
| `agent/` | The Go agent (a single binary). WebRTC plus the reverse proxy. |
| `protocol/` | The normative definition of the data channel protocol and the **shared test vectors**. |
| `docs/` | The developer and manual-test documents. |
| `scripts/` | The Windows (PowerShell) dev scripts and the test data generators. |

## Commands

With `make` installed: `make dev` / `make test` / `make lint` / `make e2e`.
For the Windows equivalents see `docs/DEV.md`.

## The fixed rules

These decisions were settled in the brief (`docs/BRIEF.md`); changing one
starts with a discussion:

1. The agent is **Go**, a single static binary.
2. The web app is **Vite + TypeScript**, with no UI framework (so the
   bundle stays small).
3. The Service Worker is written and built **by hand** —
   `vite-plugin-pwa` is not used.
4. There are **two** data channels: `ctrl` (JSON) and `data` (binary).
5. The binary framing payload limit is **16 KiB**.
6. The security principles in `docs/BRIEF.md` section 6 hold in every
   phase (they appear under the same numbers in the public README). In
   particular: the agent only makes requests to the targets given on the
   CLI (deriving a host from the path is forbidden).
7. Signaling content is always encrypted with **NIP-44**; relays only see
   "a pubkey received something".
8. The visitor is untrusted: the replay, clock skew, rate limit and
   session limit guards live in `agent/internal/connect/guard.go`.

## Changing a protocol

There are two protocols and the same rule covers both:

| What | Definition | Vectors | TS | Go |
|---|---|---|---|---|
| Data channel | `protocol/README.md` | `testvectors.json` | `web/src/protocol/` | `agent/internal/protocol/` |
| Signaling | `protocol/signaling.md` | `signaling-testvectors.json` | `web/src/nostr/` | `agent/internal/signaling/` |

If you change one:

1. update the definition file,
2. add vectors to the vector file,
3. get **both** the TS **and** the Go side green against the same
   vectors.

The same pattern holds outside the protocol too: the QR encoder
(`web/src/ui/qr.ts`) is tested against
`web/src/ui/qr-testvectors.json`, and that file is produced by
`web/scripts/gen-qr-vectors.py` (the python `qrcode` package). If you
change the encoder, regenerate the vectors and get the tests green.

The signaling vectors are produced by
`protocol/gen-signaling-vectors.py` — a **third, independent
implementation** (the python `cryptography` package). That is deliberate:
Go and TS are both verified against vectors neither of them produced, so
the two cannot make the same mistake together.

A one-sided protocol change is not accepted.

## Commit messages

During the phases: `faz-<N>: a short description`. Small, single-topic
commits. `CHANGELOG.md` is updated at the end of every phase.

From phase 8 on, new commit messages are written in English; the history
before it is left as it is.

## Versions and tags (phase 4)

- The tag format is `v<major>.<minor>.<patch>` (for example `v0.1.0`).
  Pushing a tag runs `.github/workflows/release.yml`, and goreleaser
  opens the release as a **draft** — the notes are reviewed by hand and
  then published.
- The version information is injected with `-ldflags -X`; in a binary
  installed with `go install` the `runtime/debug` fallback takes over
  (`agent/internal/version`).
- The changelog stays phase based: through `0.x` the project ties versions
  to phases and keeps no separate `[x.y.z]` section.
- A local trial publishes nothing: `goreleaser release --snapshot --clean`.

## Tests

- Unit: `vitest` (web), `go test` (agent).
- E2E: Playwright (Chromium), with the agent and the test site really
  started.
- A phase does not close until its acceptance criteria are verified by
  tests.
