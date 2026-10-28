# Development

## Requirements

| Tool | Version | Note |
|---|---|---|
| Go | 1.27+ (the `go` directive in agent/go.mod) | the agent |
| Node.js | 20+ (developed on 22.12) | the web app |
| make | optional | if it is missing on Windows, use `scripts/*.ps1` |
| golangci-lint | optional | without it `gofmt` + `go vet` still run; it is mandatory in CI |
| goreleaser | optional | only for a release build: `go install github.com/goreleaser/goreleaser/v2@latest` |

First-time setup:

```sh
cd web && npm install
cd ../agent && go mod download
cd ../web && npx playwright install chromium   # for e2e only
```

## Equivalent commands

| What it does | make | npm (inside web) | PowerShell (at the repo root) |
|---|---|---|---|
| The web dev server (5173) | `make dev` | `npm run dev` | `.\scripts\dev.ps1` |
| Every unit test | `make test` | — | `.\scripts\test.ps1` |
| The web unit tests | `make test-web` | `npm test` | — |
| The agent unit tests | `make test-agent` | — | `cd agent; go test ./...` |
| Build (web + agent) | `make build` | — | `.\scripts\build.ps1` |
| A Pages build | `make build PEERPAGES_BASE=/PeerPages/` | `node scripts/build.mjs /PeerPages/` | `.\scripts\build.ps1 -Base "/PeerPages/"` |
| Lint and format checks | `make lint` | `npm run lint` | `.\scripts\lint.ps1` |
| Format | — | `npm run format` | — |
| E2E (every phase) | `make e2e` | `npm run e2e` | `.\scripts\e2e.ps1` |
| Generate the test data | `make testdata` | — | `cd agent; go run ./internal/testsite/gen` |
| A release snapshot build | `make release-snapshot` | — | `scripts/release-snapshot.ps1` |
| Validate the release config | `make release-check` | — | `goreleaser check` |

## `base` (the GitHub Pages subpath)

The app can run at `/` or under a subpath such as `/PeerPages/`. The value
is given at build time through the `PEERPAGES_BASE` environment variable
and reaches the code as `import.meta.env.BASE_URL`. The Service Worker
scope is derived from it too (`src/base.ts`).

The build goes through `scripts/build.mjs`, which passes base to the
subprocesses as an environment variable itself. That gets past both
"`VAR=x cmd` does not work on Windows" and "Git Bash turns `/PeerPages/`
into a Windows path".

> **A Git Bash warning:** when you pass base as an ARGUMENT, MSYS still
> steps in:
>
> ```sh
> MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/
> ```
>
> If the script detects such a conversion it fails with a clear message.
> In PowerShell (`scripts\build.ps1`) there is no such problem.

## The Pages deploy (phase 7)

The workflow that publishes the app shell:
`.github/workflows/pages.yml`. It runs when a change under `web/**` or
`protocol/**` reaches `main`; to trigger it by hand use the Actions tab →
"Pages" → "Run workflow".

Two repository variables (Settings → Secrets and variables → Actions →
Variables):

| Variable | Default | What it does |
|---|---|---|
| `PAGES_BASE` | `/PeerPages/` | The project page subpath; the build base |
| `PAGES_CNAME` | empty | When set, base becomes `/` and `dist/CNAME` is written |

Attaching a custom domain:

1. Create the DNS record (for a subdomain a `CNAME` →
   `<user>.github.io`; for an apex, GitHub's A/AAAA addresses).
2. Put the domain into the `PAGES_CNAME` variable (for example
   `demo.example.com`).
3. Run the workflow again. The site is now at the ROOT of the domain.
4. Update the address the agent prints too:
   `--app-url https://demo.example.com/` (the default lives in
   `DefaultAppURL` in `internal/cli/key.go`).

The technical gain of a root deploy is not just cosmetic: the local site's
**root-absolute** paths (`/style.css`) stay inside the Service Worker
scope. Under a subpath those paths fall outside the scope and the SW
cannot catch them (the structural boundary in BRIEF section 8).

The workflow does NOT repeat the tests; `ci.yml` already runs lint, the
unit tests and e2e. Tying the deploy to e2e would slow publishing down and
hide the deploy's own failure behind the noise.

## The URL layout

| Path | What |
|---|---|
| `/<base>/` | **The local site's root.** With the tunnel open the SW proxies this. With no tunnel a small bootloader is served: it installs the SW and moves on to `connect/`. |
| `/<base>/connect/` | **The connect screen and the tunnel-owner page.** The SW never sends this path through the tunnel, so the tunnel survives even if this tab is refreshed. |
| `/<base>/sw.js` | The Service Worker. It has to be unhashed and at the scope root. |
| `/<base>/assets/…` | The app shell's JS and CSS files. |

The reasoning is in BRIEF section 8 ("the app shell and the local site's
root both want the same URL"). The SW derives its scope **at runtime**
from `registration.scope` (`src/sw.ts`), not from a build-time constant.

## Why the web build has three steps

`npm run build` (that is, `node scripts/build.mjs`) runs three Vite builds
in order:

1. `vite build` — the two HTML entries (`index.html` the bootloader,
   `connect/index.html` the connect screen) and the hashed `assets/`.
2. `vite build --config vite.sw.config.ts` — the Service Worker,
   `dist/sw.js`.
3. `vite build --config vite.shim.config.ts` — the WebSocket shim,
   `dist/ws-shim.js` (phase 6). NOT a module but a classic IIFE: it is
   injected into the visitor's page and has to run before the page's own
   scripts.

The Service Worker (and the shim) are built separately because:

- the file name has to be **fixed and unhashed**
  (`register(base + "sw.js")`),
- it has to be a single file at the scope root (an extra chunk is an extra
  request),
- full control over what gets bundled is needed.

`vite-plugin-pwa` is deliberately not used (a BRIEF decision).

The later builds run with `emptyOutDir: false`, so **the order matters**:
the app shell has to come first.

## The TypeScript configuration

There are three separate `tsconfig` files, because the app and the Service
Worker run in different global environments (the `DOM` and `WebWorker`
libs cannot be used at the same time):

| File | Lib | Scope |
|---|---|---|
| `tsconfig.json` | `DOM` | `src/**` (except sw.ts) |
| `tsconfig.sw.json` | `WebWorker` | `src/sw.ts`, `src/protocol/**` |
| `tsconfig.node.json` | node | `e2e/**`, `*.config.ts`, the tests |

`npm run typecheck` runs all three together; `npm run lint` adds biome to
that.

## The agent

```sh
cd agent

# Nostr signaling (the default): the visitor only opens the URL
go run . serve --target http://localhost:3000

# With a shared secret
go run . serve --target http://localhost:3000 --secret cat-fish-7

# Several targets: the agent prints a separate visitor URL for each one
go run . serve -t http://localhost:3000 -t admin=http://localhost:4000

# Manual signaling (copy-paste); a flag picks the target
go run . serve --target http://localhost:3000 --manual
go run . serve -t http://localhost:3000 -t admin=http://localhost:4000 --manual --manual-target admin

# Your own relay list
go run . serve --target http://localhost:3000 --relay wss://nos.lol

# Diagnostics: relay health plus the NAT type (plus a TURN allocation)
go run . doctor
go run . doctor --json
go run . doctor --turn turn:example.com:3478 --turn-user u --turn-pass p

# TURN (needed on symmetric NAT; used on the AGENT side only)
go run . serve --target http://localhost:3000   --turn turn:example.com:3478 --turn-user u --turn-pass p
# Cloudflare Realtime TURN: the credentials are fetched from the API and refreshed
PEERPAGES_CF_TURN_TOKEN=... go run . serve --target http://localhost:3000   --turn-cloudflare-key-id <key>
# Diagnostics: make the direct path impossible (if it connects, it went over TURN)
go run . serve --target http://localhost:3000   --turn turn:127.0.0.1:3478 --turn-user u --turn-pass p --ice-relay-only

# The WebSocket bridge (phase 6): the shim is injected into the HTML, OFF by default
go run . serve --target http://localhost:5173 --inject-ws-shim
# The limits: the number of long-lived streams (ws + SSE) and the ws message limit
go run . serve --target http://localhost:3000 --max-streams 16 --max-ws-message 4MB

# The version and build information
go run . version

# The identity
go run . key                   # the peer-id and the visitor URL
go run . key --target admin    # the visitor URL with a target
go run . key --reveal-secret   # show the secret key too

# Verbose logging (paths are NOT MASKED)
go run . serve --target http://localhost:3000 --verbose
```

The hidden commands for the tests: `peerpages testsite` (the local test
site), `peerpages fakerelay` (an in-process Nostr relay) and
`peerpages faketurn` (an in-process TURN server, phase 5).

The test site's streaming endpoints (phase 6):
`/sse?n=5&delay=400&keepalive=1` and `/ws` (a text and binary echo;
`?proto=` sets the subprotocol, `?close=4002` makes the server close). The
SSE default is as it was in phase 1: three events, no delay.

The WebSocket bridge and the shim: [WEBSOCKET.md](WEBSOCKET.md).

The environment variables for passwords and tokens:
`PEERPAGES_TURN_PASS`, `PEERPAGES_CF_TURN_TOKEN`. Command line arguments
are visible to other users in `ps` output, which is why a flag is not the
only way.

TURN setup and its security notes: [TURN.md](TURN.md).

By default the logs mask URL paths (`/api/items` → `/*3/*5`);
`--verbose` turns that off (BRIEF security principle 6).

The agent makes requests **only** to the hosts given with `--target`.
Deriving a host from the request path was made deliberately impossible
inside `internal/target`, and tests keep it that way (principle 1).

## Releases (phase 4)

The version information is injected with `-ldflags -X` into
`main.buildVersion/buildCommit/buildDate`; with none given,
`runtime/debug.ReadBuildInfo` is used (`internal/version`). So a binary
installed with `go install ...@latest` prints the right version too.

```sh
goreleaser check                       # is the configuration valid
goreleaser release --snapshot --clean  # 6 archives under dist/, it DOES NOT PUBLISH
```

A real release: pushing a tag of the form `v*` runs
`.github/workflows/release.yml`, and goreleaser opens the release as a
**draft** (the notes are reviewed by hand). The Scoop and Homebrew drafts
sit in `.goreleaser.yaml` but are off with `skip_upload: true`: the tap
repositories do not exist yet. The project name was settled in phase 7, so
the module path and the binary name are fixed now.

End-user installation: `docs/INSTALL.md`.
