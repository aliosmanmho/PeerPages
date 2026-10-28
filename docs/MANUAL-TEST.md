# Trying it by hand

There are two paths:

- **Nostr (the default, the real path):** the visitor only opens the URL.
  No copy-paste. Steps 1–6 below describe it.
- **Manual (`--manual`):** you carry the signaling yourself. Useful where
  Nostr does not work, or when you want to isolate the chain. In the
  "Manual signaling" section.

The automated counterpart: `cd web && npx playwright test` (39 tests
across the manual path, the Nostr path and the phases that followed). With
real relays:

```bash
cd web
PEERPAGES_E2E_RELAYS="wss://nos.lol,wss://nostr.mom" npx playwright test nostr.spec.ts
```

> **A note on direction.** The roles swapped in phase 2: the **visitor**
> creates the offer and the agent returns the answer. The reason: on Nostr
> the agent cannot know the visitor's ephemeral pubkey in advance, so it
> cannot send the first message. The manual path uses the same direction
> so there is a single code path (BRIEF section 8).

> **The language of the screen.** The connect screen is English by
> default and Turkish can be chosen from the picker in the header; the
> quotes below are the English texts. To open it directly in Turkish, add
> `?lang=tr` to the address.

## 0. Preparation (once)

```bash
cd web && npm install
cd ../agent && go mod download
```

You will open two terminals. The commands run from the `agent/` and
`web/` directories.

## 1. Run the site you want to share

If you have your own project, run that (for example `npm run dev` →
`localhost:3000`) and move on to step 2. Otherwise use the test site in
the repo:

```bash
cd agent
go run . testsite --port 3000
```

In the output you will see this:

```
---PEERPAGES-TESTSITE-LISTENING--- http://127.0.0.1:3000
big.bin sha256: f2793bdc…
```

The test site has HTML, CSS, JS, a PNG, an `/api/items` JSON endpoint,
`/redirect` (302) and `/sse`; plus the 5 MB `big.bin` (generated on the
first run).

## 2. Serve the web app

There are two options.

**a) The quick path — the dev server** (`localhost:5173`, base `/`):

```bash
cd web
npm run dev
```

**b) A GitHub Pages-like subpath** (`localhost:4173/PeerPages/`) — closer
to the real deployment, and it exercises the subpath behaviour too:

```bash
cd web
node scripts/build.mjs /PeerPages/
npx vite preview --port 4173
```

> If you are using Git Bash, write the build line as
> `MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/`; without that
> MSYS turns `/PeerPages/` into a Windows path.

Below, (b) is assumed. For (a), read the addresses as
`http://localhost:5173/…`.

## 3. Check the relays (recommended)

```bash
cd agent
go run . doctor
```

For every relay it runs a connection, a NIP-11 and an **ephemeral echo**
test. The last one matters most: a relay can accept an event with "OK" and
drop it silently, and then signaling does not work while no error shows
up. It also compares the external port two STUN servers see and gives a
NAT type guess.

If you see "NAT guess: symmetric", make a note: if the other side is
behind symmetric NAT too, no direct connection can be made and TURN is
needed. The **advice line** at the end of the output joins the NAT verdict
with the TURN state and says what to do in one sentence
([TURN.md](TURN.md)).

## 4. Start the agent in Nostr mode

```bash
cd agent
go run . serve --target http://localhost:3000 --app-url "http://localhost:4173/PeerPages/"
```

On the first run `~/.peerpages/key` is created (as an nsec). **Back that
file up:** if you lose it your peer-id, and so the URL you shared,
changes.

The output:

```
The PeerPages agent is ready.

  peer-id : npub1yl4dshp…pdnn
  relays  : 4/4 connected (the status line shows this live)
  secret  : none (anyone who knows the peer-id can connect)
  ice     : stun 2 (…); no turn
  ws      : off (--inject-ws-shim turns it on)

The published targets and the addresses the visitor should open:

  default  http://localhost:3000
           http://localhost:4173/PeerPages/#npub1yl4dshp…

Keep this tab or terminal open: the tunnel is up as long as the agent runs.
---PEERPAGES-READY---
```

The "ready" line is printed **after it really starts listening** (it
connects to the relays first, then subscribes). That order matters:
because ephemeral events are not stored, an offer that arrives earlier is
lost with no way back.

If you want, add a shared secret — someone who knows the peer-id but not
the secret cannot connect:

```bash
go run . serve --target http://localhost:3000 --secret cat-fish-7 \
  --app-url "http://localhost:4173/PeerPages/"
```

The URL becomes `#npub1…:cat-fish-7`. The hash never goes to the server,
so the secret does not reach GitHub Pages.

## 5. As the visitor: just open the URL

Paste the address the agent printed into a browser. The expected flow, in
the status line:

```
Service Worker → Signaling → ICE → Tunnel
```

Within about a second the "Tunnel open" panel appears. **No copy-paste.**
This tab owns the tunnel, so do not close it.

If you want to try your own relay list (BRIEF section 4: the user can
change the list):

```
http://localhost:4173/PeerPages/connect/?relay=wss://nos.lol&relay=wss://nostr.mom#npub1…
```

## 6. Browse the site — a checklist

The **"open through the tunnel"** link on the connect screen opens the
site in a **new tab** (`http://localhost:4173/PeerPages/`). The
tunnel-owner tab stays open.

In the new tab, check these in order:

| # | What | Expected |
|---|---|---|
| 1 | The page title | "PeerPages test site" is shown |
| 2 | CSS | The background is dark (`rgb(13,17,23)`) and the heading is blue |
| 3 | JS | The "API" list shows `alfa = 1`, `beta = 2`, `gama = 3` |
| 4 | The image | The 64×64 checkered logo loaded |
| 5 | fetch/JSON | Item 3 already proves this (`/api/items`) |
| 6 | The redirect | `…/PeerPages/redirect` comes back to the site and the address bar does **not** jump to `localhost:3000` |
| 7 | The 5 MB download | `…/PeerPages/big.bin` downloads, 5,242,880 bytes |
| 8 | SSE | `…/PeerPages/sse` shows three `data: event-N` lines |

In DevTools → Network you will see the requests coming from the "Service
Worker" and that **no** request went to `localhost:3000`. The request logs
scroll in the agent's terminal (the paths are masked: `/*3/*5`; use
`--verbose` to disclose them).

## 7. What happens when the tunnel closes

1. **Close** the tunnel-owner tab (`/connect/`).
2. **Refresh** the site tab.

Expected: it falls back to the connect screen
(`/PeerPages/connect/`). With no tunnel to find, the Service Worker
redirects the navigation there; subresource requests answer `503`.

On the Nostr path the agent **keeps running**: when a new visitor opens
the URL it reconnects, and you do not have to restart the agent. On the
manual path (`--manual`) the agent's terminal shows
`---PEERPAGES-TUNNEL-CLOSED---` and the process exits.

---

## Manual signaling (`--manual`)

For when you want to skip Nostr. The direction: the **page** creates the
offer and the agent prints the answer.

### Start the agent

In the second terminal:

```bash
cd agent
go run . serve --manual --target http://localhost:3000
```

The output:

```
Open the visitor page (the connect screen), press "Create offer", then
paste the offer it produced below and press Enter.

---PEERPAGES-OFFER-WAITING---
```

The agent is now waiting for an offer from you. Leave the terminal open.

### Open the connect screen and create an offer

In the browser:

```
http://localhost:4173/PeerPages/connect/
```

- The **Service Worker** step in the status line has to turn green
  ("Service Worker active").
- Press **Create offer**. Within a few seconds (ICE gathering) a
  single-line base64 offer appears in the field below.
- Press **Copy**.

> **Do not close this tab.** This page holds the WebRTC connection; there
> is no WebRTC inside a Service Worker (BRIEF section 2). If the tab
> closes, the tunnel closes.

### Carry the offer to the agent and the answer to the page

1. Switch to the agent's terminal, **paste** the offer and press
   **Enter**.
2. The agent prints the answer:

   ```
   ---PEERPAGES-ANSWER-BEGIN---
   eyJ0eXBlIjoiYW5zd2VyIiwic2RwIjoi…
   ---PEERPAGES-ANSWER-END---
   ```

3. Copy the **single line** between the two markers, paste it into the
   "2. Paste the agent's answer" field on the page, and press **Apply
   answer**.

The agent writes:

```
---PEERPAGES-TUNNEL-OPEN---
time=… level=INFO msg="tunnel open" target=default
```

The "Tunnel open" panel appears on the connect screen: the request count,
the bytes transferred, the latency and the target list.

## Several targets (phase 3)

One agent can share several local projects:

```bash
cd agent
go run . serve   --target http://localhost:3000   --target admin=http://localhost:4000   --app-url "http://localhost:4173/PeerPages/"
```

The agent prints a separate address for every target:

```
  default  http://localhost:3000
           http://localhost:4173/PeerPages/#npub1yl4dshp…

  admin    http://localhost:4000
           http://localhost:4173/PeerPages/#npub1yl4dshp…/admin
```

The checklist:

- Open the two addresses **in two separate tabs**: each one has to show
  its own project. Each tab builds its own tunnel.
- On the connect screen the active target appears next to the peer-id
  (`… / admin`), and in the measurements the "Targets" row lists both
  targets together (that list comes from the ctrl `hello` message).
- Put a target that does not exist into the URL
  (`#npub1…/nosuchtarget`): it must not connect and it has to say
  "This agent has no 'nosuchtarget' target". It does **not fall back to
  `default` silently** — saying so is better than you not noticing you
  are browsing the wrong project.
- On the manual signaling path there is no URL, so a flag picks the
  target: `--manual --manual-target admin`.

> Changing the target in the same tab is not possible: the target is fixed
> per session, and changing it means a new address (a new connection).

## An idle session (phase 3)

Once the visitor is gone the agent has to release its resources:

1. Open a tunnel, then close the tunnel-owner tab.
2. In the agent's log (stderr) you should see `tunnel closed` or
   `session idle, closing` within about 30 seconds at the latest.
3. The `sessions` count in the `[status]` line has to go back to 0.

Instead of closing the tab, also try **killing the browser from the task
manager**: in that case the page's "I am leaving" notice never goes out,
so the 30-second check is the only protection. That is the real test.

## Trying it with two devices (phase 2's real acceptance criterion)

**You have to verify this one for me:** connecting two separate devices on
two separate networks cannot be tested from one machine.

With Nostr there is no signaling to carry — all you have to carry is the
URL.

1. **On the laptop**, run the site you want to share and the agent (steps
   1 and 4). Make the `--app-url` value an address the phone can reach.
2. **On the laptop**, expose the web app to the network:

   ```bash
   cd web
   node scripts/build.mjs /PeerPages/
   npx vite preview --port 4173 --host
   ```

   > **In Git Bash** write the build line as
   > `MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/`.

3. Replace `localhost` in the URL the agent printed with the laptop's
   network address and **open it on the phone**. The easiest way to carry
   the URL is the **QR**: once the tunnel is open on the laptop, press the
   **QR code** button on the connect screen and scan it with the phone's
   camera (there is a "Copy address" button too).

   > **This step is the only thing that verifies the QR really scans.**
   > The encoder is tested byte for byte against a third, independent
   > implementation's vectors and the canvas being drawn is checked in
   > e2e, but an automated test cannot answer "does a phone camera read
   > this". If it does not scan, say so.

   > **A Service Worker wants a secure context.** `localhost` and
   > `127.0.0.1` count as secure but **a bare IP address does not**. If
   > the Service Worker does not install on the phone, the options are:
   > - use a server that gives HTTPS (with your own certificate),
   > - add that address to
   >   `chrome://flags/#unsafely-treat-insecure-origin-as-secure` in
   >   Chrome (for testing only),
   > - in the real deployment from phase 7 there is no problem: GitHub
   >   Pages gives HTTPS.

4. On the phone the page has to connect by itself: the status line moves
   through `Service Worker → Signaling → ICE → Tunnel`.

Connecting **from different networks** (the phone on mobile data, the
laptop on home Wi-Fi) works on most NATs: **both sides** use STUN now
(until phase 5 only the agent did). But `doctor` saw **symmetric NAT** on
this network: if the other side is behind symmetric NAT too, no direct
connection can be made. Then you see the error "The data channels did not
open" — not a software bug but a consequence of the network topology. The
remedy is TURN: the "Trying it with TURN (phase 5)" section.

## Sleep/wake (phase 4)

**This is the real attempt at the acceptance criterion.** The automated
test produces the same result by restarting the agent, but a real suspend
is not the same thing: the clocks, the WebSockets and the operating system
are all involved.

1. Start the agent, open the visitor URL, and see the tunnel open.
2. Browse the site in the visitor tab (make a few requests).
3. **Put the laptop to sleep** (close the lid, or `Win+X → Shut down →
   Sleep`). Wait at least **2 minutes**: that passes both the 30-second
   idle limit and the upper bound of the backoff.
4. Wake it and look at the agent's terminal.

Expected:

- The agent must **not have died**. The `[status]` line keeps scrolling.
- In the agent's log you may see
  `the check ran late, the process may have been suspended` — that is the
  right behaviour: a session is closed because a ping went unanswered, not
  because the clock jumped.
- You will see a field like `relays 3/4`; relays that went down after the
  wake should come back within a few seconds (the log:
  `the relay connections changed`).
- On the connect screen the text "Connection lost — reconnecting" appears
  and it goes back to "Tunnel open" **by itself**. Do not click anything.
- A **Reconnects: 1** (or more) row appears in `#metrics`; the request and
  byte counters are **not reset**.
- Refresh the visitor tab: the site has to work again.

If it does not: note the error text on screen and the last 20 lines of the
agent's log. The most likely cause is the relay side rather than the agent
side (run `doctor`).

## A relay going down (phase 4)

The agent must not depend on a single relay, and it has to recover when
the relay comes back.

1. Start the agent with **your own** relay (you need a relay you can take
   down):

   ```sh
   # terminal 1: the fake relay on a fixed port
   cd agent && go run . fakerelay --port 7447

   # terminal 2
   cd agent && go run . serve --target http://localhost:3000 --relay ws://localhost:7447
   ```

2. Open the visitor URL with `?relay=ws://localhost:7447` added and see
   the tunnel open.
3. **Kill** the fake relay (Ctrl+C in terminal 1).

   Expected: the agent **does not exit**. The `[status]` line shows
   `relays 0/1` and the log carries the warning
   `no connection to any relay: visitors cannot reach you right now`. The
   open tunnel keeps working — WebRTC does not depend on the relay, and
   signaling was already finished.
4. Restart the fake relay **on the same port**.

   Expected: `relays 1/1` and `every relay is connected` in the log. You
   should now be able to open a new visitor tab: the subscription must
   have been renewed.

## Verifying the installation (phase 4)

```sh
peerpages version
```

Expected: the version, the commit, the date, the platform and the Go
version. If you built from source with `go build`, the version shows as
`dev (<commit>)`; a release binary carries a tag such as `v0.1.0`.
Details: `docs/INSTALL.md`.

## Trying it with TURN (phase 5)

On this network `doctor` sees **symmetric NAT**: if the other side is
behind symmetric NAT too, no direct connection can be made. TURN is the
way out of that. Detailed setup and security notes: [TURN.md](TURN.md).

### (i) A five-minute local trial (no real TURN server needed)

```bash
# terminal 1: the in-process TURN server
cd agent && go run . faketurn --port 0 --user u --pass p
# Take the address it prints: turn:127.0.0.1:XXXXX

# terminal 2: make the agent gather relay candidates ONLY
cd agent && go run . serve --target http://localhost:3000   --turn turn:127.0.0.1:XXXXX --turn-user u --turn-pass p --ice-relay-only
```

Open the URL the agent printed. Expected:

- The tunnel opens (the `ice` row in the banner says
  `turn 1 … RELAY ONLY`).
- The **Connection path** row in `#metrics` says
  **`through TURN (relay)`**.
- The site works normally.

`--ice-relay-only` makes the direct path **impossible**, so if the tunnel
opened the traffic must have gone over TURN. It is a diagnostic flag and
is not needed in production.

For comparison, drop `--ice-relay-only` and try again: with the same TURN
defined, the **Connection path** should come out `direct` (ICE picks the
right path and no quota is wasted).

### (ii) The real criterion: two networks (a mobile hotspot ↔ Wi-Fi)

**You have to verify this one for me.** It needs a real TURN server
(coturn or Cloudflare — [TURN.md](TURN.md)).

1. Run `peerpages doctor --turn <address> --turn-user … --turn-pass …`.
   In the `TURN` section you should see a **relay address**, and the
   advice line should say "The NAT is symmetric but TURN is ready".
2. Start the agent with the same TURN details (**do NOT** add
   `--ice-relay-only`: in real life, if the direct path exists it should
   be used).
3. Put the phone **on mobile data** (leave the laptop on Wi-Fi) and open
   the visitor URL on the phone (the QR is the easiest way).
4. Look at the **Connection path** row on the connect screen.

Expected: the tunnel opens. If the row says `direct (through NAT)`, this
pair of networks did not need TURN (good news); if it says
`through TURN (relay)`, the exact case phase 5 solves really happened.
Both are a success — the only failure is the error "The data channels did
not open".

> **No TURN credential reaches the phone.** The visitor knows nothing
> about TURN; the agent opens the relay and the phone only sends packets
> to that public address. So whoever you share the URL with cannot spend
> your quota.

## Trying it with WebSocket (phase 6)

The WebSocket bridge is **off by default** (it interferes with the HTML).
It takes one flag to turn on: `--inject-ws-shim`.

### (i) Vite HMR — five minutes

```bash
# terminal 1: any Vite project
npm create vite@latest trial -- --template vanilla
cd trial && npm install && npm run dev

# terminal 2: the agent (with the shim on)
cd agent && go run . serve --target http://localhost:5173 --inject-ws-shim
```

Open the URL the agent printed, then browse the site in the visitor tab.
Now change a piece of text in `trial/src/main.js` and save:

- [ ] The page updated **without reloading** (no load in the address
      bar).
- [ ] The tunnel tab's `#metrics` carries a **"WebSocket: 1 open"** row.
- [ ] DevTools → Console says `[vite] connected.`

> **Root-absolute paths.** On a project page
> (`user.github.io/PeerPages/`) the paths Vite produces, such as
> `/src/main.js`, fall outside the scope. The remedy is
> `base: "/PeerPages/"` in `vite.config.js` plus the same prefix on the
> agent's target: `--target http://localhost:5173/PeerPages/`. The
> reasoning and the details: [WEBSOCKET.md](WEBSOCKET.md).

### (ii) Socket.IO — the real manual criterion

This path was **not tested automatically** (BRIEF section 8); verifying it
is yours.

```bash
mkdir io-trial && cd io-trial && npm init -y && npm i socket.io
```

`server.js`:

```js
const { createServer } = require("node:http");
const { Server } = require("socket.io");

const http = createServer((req, res) => {
  res.setHeader("content-type", "text/html; charset=utf-8");
  res.end(`<!doctype html><html><head><title>io</title></head><body>
    <h1 id="state">connecting…</h1>
    <script src="/socket.io/socket.io.js"></script>
    <script>
      const s = io();
      s.on("connect", () => { document.getElementById("state").textContent = "connected: " + s.id; });
      s.on("clock", (t) => { document.getElementById("state").textContent = t; });
      s.on("disconnect", (r) => { document.getElementById("state").textContent = "dropped: " + r; });
    </script></body></html>`);
});

const io = new Server(http);
io.on("connection", (sock) => {
  const timer = setInterval(() => sock.emit("clock", new Date().toISOString()), 1000);
  sock.on("disconnect", () => clearInterval(timer));
});
http.listen(3100, () => console.log("http://localhost:3100"));
```

```bash
node server.js
# in another terminal:
cd agent && go run . serve --target http://localhost:3100 --inject-ws-shim
```

In the visitor tab:

- [ ] The heading becomes "connected: <id>", then the clock updates once a
      second.
- [ ] DevTools → Network → the WS tab shows **no real WS connection** (the
      shim took it into the tunnel; the only thing you may see is the
      first polling requests).
- [ ] The tunnel tab carries the "WebSocket: 1 open" row.
- [ ] When you stop the agent with Ctrl+C the page says
      "dropped: transport close", and when you start it again Socket.IO
      reconnects by itself.

### (iii) SSE — a long stream is no longer cut off

Until phase 6 the agent's 60-second timeout wrapped **the whole
response**, so a long-lived SSE stream was cut off silently. The test
endpoint:

```bash
cd agent && go run . testsite --port 3000      # terminal 1
cd agent && go run . serve --target http://localhost:3000   # terminal 2
```

In the visitor tab's console:

```js
const es = new EventSource("sse?n=0&delay=5000&keepalive=1");
es.onmessage = (e) => console.log(new Date().toISOString(), e.data);
```

- [ ] The events arrive every 5 seconds and **keep coming after the
      60-second mark** (wait at least 90 seconds).
- [ ] When you close the tab the stream closes in the agent's log (no
      resource leak).

## The language of the page (phase 8)

The connect screen is English by default and Turkish can be chosen.

1. Open the connect screen with no query string: the header has to say
   "Serverless tunnel" and the picker has to show "English".
2. Pick "Türkçe" from the picker: the whole screen switches language and
   `<html lang>` becomes `tr`.
3. Refresh the page: it has to come back in Turkish (the choice is kept in
   `localStorage`).
4. Open `?lang=en`: the query string wins over the stored choice.
5. Open `?lang=klingon`: it has to fall back to English without an error
   screen (there is a note in the console).

The automated counterpart: `cd web && npx playwright test i18n`.

> The agent's CLI output and its logs are English only, on purpose: a
> translated log breaks searchability and debugging. The four plain-text
> bodies the Service Worker writes when there is no tunnel stay English
> too (BRIEF section 8).

## Troubleshooting

| Symptom | Cause / remedy |
|---|---|
| The status line is stuck on "Starting…" | The Service Worker could not be installed. Is this a secure context (`localhost`/HTTPS)? Look at DevTools → Application → Service Workers. |
| "base64 could not be decoded" | The text was copied incompletely. Take the **whole** line between the two markers. |
| The offer field stays empty | ICE gathering may not have finished; wait 10 s. Is there an error in the console? |
| The agent says "the input could not be read: the input ended" | The agent closed before you pasted and pressed Enter. Start it again. |
| "The agent did not answer" | The agent is not running, or the relays are not delivering the message. Run `go run . doctor`: are the relays "usable"? |
| "The shared secret does not match" | The secret in the URL is not the same as the agent's `--secret` value. |
| "The data channels did not open" | Signaling worked but WebRTC could not come up. Most likely symmetric NAT. Look at `doctor`'s advice line; pass a TURN server with `--turn` ([TURN.md](TURN.md)). |
| I gave TURN and it still did not work | Run `peerpages doctor --turn …`: is an allocation granted? If not, the address or the credentials are wrong, or UDP is blocked (try `turns:`). |
| The connection path says "through TURN" | The expected behaviour: no direct path could be built and the traffic goes over the relay. The encryption continues (DTLS), but the quota is being spent. |
| "the relays could not be reached" | The network may be blocking WebSocket. Pass your own relay with `--relay`. |
| A peer-id error | You did not copy everything after the `#` in the URL. An npub is 63 characters. |
| The site tab falls back to the connect screen | The tunnel-owner tab is closed. Make sure the `/connect/` tab is open. |
| Subresources answer 503 | The same cause: there is no tunnel. If the connect screen says "reconnecting", wait a few seconds: during a drop the SW holds a request for a while (10 s for a subresource, 3 s for a navigation) before falling back to 503. |
| "reconnecting" never ends | The agent really is down, or the relays cannot be reached. Look at the agent's terminal: does it say `relays 0/N`? |
| The tunnel does not come back after a wake | While the tab is in the background the backoff slows down; switch to the tab (focus resets the backoff) or press "Try now". |
| Root-absolute paths (`/style.css`) 404 | A structural limit of sites served under a subpath (`/PeerPages/`): that path falls outside the Service Worker's scope. With a custom domain or at the root of `user.github.io/` there is no problem (BRIEF section 8). |
| WebSocket `onclose` code 1008 | The bridge is off: start the agent with `--inject-ws-shim`. If the flag was given, the local server refused the handshake (check the path). |
| WebSocket `onclose` code 1011 | The local server was unreachable: is it up, is the path right? |
| WebSocket `onclose` code 1013 | The stream budget is full; raise `--max-streams`. |
| WebSocket `onclose` code 1009 | The message exceeded the `--max-ws-message` limit. |
| Vite HMR does not connect | If the console says `[vite] failed to connect`: lining up `base` (the note above), or the page's CSP. |
| The page reloads but HMR does not work | In some frameworks a change requires a full reload; that is HMR's own behaviour, not a problem with the tunnel. |
| The 5 MB download is very slow | With the browser and the agent on one machine it should be around 100 ms. On different networks it depends on the connection speed. |
