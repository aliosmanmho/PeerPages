# WebSocket and SSE (phase 6)

## The short answer

- **SSE (`text/event-stream`) works out of the box**, no flag needed.
- **WebSocket takes one flag:** `--inject-ws-shim`. It is **off** by
  default, because it interferes with the page's HTML.

```bash
peerpages serve --target http://localhost:5173 --inject-ws-shim
```

## Why a separate bridge was needed

A Service Worker only catches `fetch` events; **the WebSocket handshake is
not in its reach**. So without the shim, a `new WebSocket(...)` call on a
tunneled page goes to the real network and fails. Vite HMR, Socket.IO and
every dev server with a live preview quietly half-work over the tunnel:
the page shows up but no live update arrives.

## How it works

```
visitor tab                               tunnel-owner tab          your machine
┌───────────────────────┐                 ┌──────────────┐          ┌───────────┐
│ page + ws-shim.js     │ MessageChannel  │ RTCPeerConn. │  ws-open │ localhost │
│  new WebSocket(...) ──┼──► SW ──port──► │  data channel├─────────►│   :5173   │
└───────────────────────┘                 └──────────────┘  ws load └───────────┘
```

1. When the agent runs with `--inject-ws-shim` it adds an
   `x-peerpages-ws-shim: 1` header to HTML responses. **It does not touch
   the body.**
2. When the Service Worker sees that header it injects
   `<script src="…/ws-shim.js">` into the HTML stream after `<head>` and
   serves that file from its own bundle (it never goes through the
   tunnel).
3. The shim wraps `window.WebSocket`. For the addresses it takes into the
   tunnel it sets up a `MessageChannel` and sends the port to the SW; the
   SW finds the tunnel-owner tab and **transfers** the port to it. From
   then on it is tab ↔ tab: the SW may be killed while idle, and a
   long-lived socket cannot depend on it.
4. There are three control messages in the tunnel (`ws-open`, `ws-ok`,
   `ws-close`); the payload travels on the `data` channel with a flag byte
   in the frame body (text/binary plus last-chunk). Details:
   `protocol/README.md`.

> **Why does the SW do the injection rather than the agent?** The README's
> first plan had the agent rewriting the HTML. The side that knows the
> scope prefix (`/PeerPages/`) is the SW, not the agent; and this choice
> also keeps `internal/proxy` **byte-for-byte transparent** and the
> protocol version unbroken. The policy lives in the agent (the flag), the
> mechanics in the SW. A departure from BRIEF; the reasoning is in BRIEF
> section 8.

## Which addresses the shim takes into the tunnel

| Address | What happens |
|---|---|
| `new WebSocket("ws")` (relative) | goes through the tunnel |
| `wss://<your-page>/path` (the same hostname) | goes through the tunnel |
| `wss://<your-page>:5173/` (the same hostname, a different port) | goes through the tunnel |
| `wss://another-site.example/` | goes to the **real** `WebSocket` |

Why the port does not matter: Vite HMR can put its own port in the
address, but that port is meaningless in the visitor's browser — the
target is decided by the agent's allowlist (BRIEF principle 1). We never
touch other hostnames (the shim's counterpart of principle 5).

## Vite HMR

It needs **no configuration at all**: Vite's client derives the ws address
from the page's own origin, and the shim takes that address into the
tunnel. It is verified in the e2e test (`web/e2e/phase6.spec.ts`): when a
source file changes the page updates **without reloading**.

Two things are still worth knowing:

- **Lining up `base`.** The **root-absolute** paths the dev server
  produces (`/src/main.js`) fall outside the scope on a GitHub Pages
  project page, and the SW does not catch them (the structural boundary in
  BRIEF section 8). The remedy: make the dev server's `base` the same as
  the scope root and give the agent's target that prefix too:

  ```bash
  # vite.config.js: base: "/PeerPages/"
  peerpages serve --target http://localhost:5173/PeerPages/ --inject-ws-shim
  ```

  On a custom domain or a user page (`user.github.io/`) the scope is the
  root, so this is not needed.
- **`hmr.clientPort`.** If Vite puts its own port in the ws address the
  shim still takes it into the tunnel (the same-hostname rule). If
  something goes wrong, `server.hmr.clientPort: 443` settles it.

## Socket.IO

It works but is **not tested automatically** (it would bring two new
dependencies):

- The long-polling (`polling`) phase is ordinary HTTP, so it already went
  through the tunnel; before phase 6 the 60-second timeout caught that too,
  and it no longer does.
- The WebSocket upgrade goes through the shim. Binary payloads
  (`binaryType`) are supported.
- If the server connects the client from the same origin with
  `io(location.origin)`, no configuration is needed.

The manual trial steps: `docs/MANUAL-TEST.md` → "Trying it with WebSocket
(phase 6)".

## Limits and deliberate decisions

- **One flag.** `--inject-ws-shim` turns on the bridge **and** the
  injection together: without the shim no browser page sends a `ws-open`,
  and without the bridge the shim is useless. With the flag omitted,
  incoming `ws-open` requests are refused with `ws-close 1008`.
- **`bufferedAmount` is always 0.** The real send buffer is inside the
  tunnel; having the shim imitate that value would be misleading. The
  practical effect: libraries that wait for `bufferedAmount` to drop never
  wait.
- **No extension negotiation.** The subprotocol
  (`Sec-WebSocket-Protocol`) passes through; extensions such as
  `permessage-deflate` do not (`extensions` is an empty string).
- **`Origin` goes out as the target's own origin.** Local servers (Vite,
  Socket.IO) check `Origin` and would refuse the visitor's `github.io`
  origin. So the tunnel looks like **a local client** to the local server.
  That is a deliberate choice and a limit: the local server's origin check
  cannot tell traffic from the tunnel apart (BRIEF section 8).
- **If the tunnel drops, open sockets go down** (code 1006). The phase 4
  supervisor builds a new session but does not bring the sockets back; the
  reconnect is left to the page's own library (Vite and Socket.IO both do
  it).
- **The message limit** is `--max-ws-message` (1MB by default), in both
  directions. The visitor is untrusted (BRIEF principle 4): an unbounded
  message could blow up the agent's memory in a single request. Exceeding
  it gives `ws-close 1009`.

## The long-lived stream budget

`--max-streams` (8 by default) bounds the number of **ws sessions plus SSE
responses**:

```bash
peerpages serve -t http://localhost:3000 --inject-ws-shim --max-streams 16
```

Why it is separate from `--max-concurrent`: a long-lived stream holds its
slot for its whole life, so with a single budget 64 SSE requests would
make the tunnel unusable for ordinary requests. When the budget is full an
SSE request gets `abort too-many-streams` and a ws request gets
`ws-close 1013` — both say why.

An HTTP response is **moved** to the stream budget the moment
`content-type: text/event-stream` is seen: we cannot know in advance which
response is long-lived, and that is the only honest hint.

## The one thing to know about SSE

Before phase 6 the agent's 60-second timeout wrapped **the whole
response**: every SSE stream living longer than 60 seconds was cut off
silently — the visitor did not even see an error, just "the stream
ended". That deadline now covers only the arrival of the **response
head**; while the body streams there is no time limit. What ends a stream:
the visitor giving up, the session closing, or the local server ending the
stream.

The test endpoint: `/sse?n=5&delay=400&keepalive=1` in
`peerpages testsite`.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `onclose` code **1008** | The bridge is off (no `--inject-ws-shim`), or the local server refused the handshake (check the path) |
| `onclose` code **1011** | The local server was unreachable (is it up, is the path right) |
| `onclose` code **1009** | The message exceeded the `--max-ws-message` limit |
| `onclose` code **1013** | The stream budget is full (`--max-streams`) |
| `onclose` code **1002** | A protocol error: a broken frame or a broken order. That is a bug; share the agent log with `--verbose` |
| The shim never loads | The response is not HTML (only `text/html` and `application/xhtml+xml` are marked), or the flag was not given |
| Vite HMR does not connect | Lining up `base` (above), or the page's CSP may have narrowed `script-src` |
| SSE is cut off at 60 seconds | You are using an agent from before phase 6 (`peerpages version`) |
