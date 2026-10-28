# Launch material

Ready-made texts and a checklist. **None of it has been posted** — the
decision to post, and when, is yours.

The rule for the tone is the project's own rule: **no exaggeration, no
claim that has not been measured.** Hacker News and r/selfhosted will try
to puncture the "no server" claim in the first comment; hand them the
material yourself and you win.

---

## 1. First: the 10-second GIF

The two screenshots at the top of the README are real but **static**. What
really convinces is a URL coming out of one terminal and opening on
another device. Recording that from your own machine beats any synthetic
animation: real latency, real mouse movement, a real site.

**The script (target: 10–12 seconds)**

| Second | On screen |
|---|---|
| 0–2 | The terminal: `peerpages serve --target http://localhost:3000` is typed and runs |
| 2–4 | The agent becomes ready and the visitor URL appears (you do not need to blur the peer-id, it is the same on every run anyway — use a temporary key with `--key` if you prefer) |
| 4–6 | The URL is copied and opened **in a second browser window** (ideally a mirrored phone screen) |
| 6–9 | The connect screen's steps go by, the site appears, and a click or two browses it |
| 9–12 | A piece of text is changed in the local editor, the page is refreshed, and the change shows up for the visitor |

**How to record it**

- Windows: [ScreenToGif](https://www.screentogif.com/) — pick the window,
  15 fps, then "Reduce frame count" plus 256 colours.
- macOS: a screen recording with QuickTime →
  `ffmpeg -i demo.mov -vf "fps=15,scale=900:-1" demo.gif`.
- Linux: `peek`, or the same `ffmpeg` command.

**The target:** 900 px wide and **under 2 MB** (GIFs over 2 MB load slowly
in a GitHub README). Put the file at `docs/img/demo.gif` and replace the
table at the top of the README with this:

```markdown
![PeerPages demo](docs/img/demo.gif)
```

> Do not delete the screenshots: move them below the GIF so the table
> stays as a fallback where GIFs do not load (RSS, some mobile clients).

---

## 2. Show HN

**Title candidates** (HN titles should be plain; emoji and exclamation
marks hurt):

1. `Show HN: PeerPages – Share your localhost over GitHub Pages, no server in between`
   — the clearest one. The "no server" claim starts the discussion, which
   is what we want.
2. `Show HN: Tunnel to localhost with WebRTC and Nostr signaling, no relay server`
   — puts the technology first; it draws the WebRTC/Nostr crowd but pushes
   "what is it for" into second place.
3. `Show HN: A localhost tunnel where the traffic never touches a third party`
   — puts the benefit first; the least technical and the most open to
   being misread.

**Recommended: 1.**

**The first comment** (an HN convention: the author gives the context in
the first comment)

> I built this because every localhost tunnel I used terminates TLS on
> someone else's machine. ngrok and Cloudflare Tunnel are good products, but
> the provider can read the traffic by construction — that is how they route
> it.
>
> PeerPages has no server in the path. The page is static on GitHub Pages, the
> WebRTC handshake goes through public Nostr relays as NIP-44-encrypted
> ephemeral events, and the data is a direct DTLS connection between the
> visitor's browser and a small Go agent on my machine. The visitor installs
> nothing; a Service Worker makes relative links, CSS, XHR, SSE and (opt-in)
> WebSockets work transparently.
>
> What I think is interesting technically:
>
> - The Service Worker can't hold the WebRTC connection (no WebRTC in workers),
>   so the page holds it and the SW forwards each request over a MessageChannel.
> - `ctrl` and `data` are two SCTP streams with no ordering guarantee between
>   them. I measured `end` arriving before six body frames, so the end message
>   carries frame and byte counters instead of meaning "done".
> - WebSocket handshakes are invisible to `fetch`, so there is an opt-in shim
>   that wraps `window.WebSocket` and tunnels the payload over the same data
>   channel. Vite HMR works through it; that's covered by an end-to-end test
>   that edits a file and asserts the page updated without reloading.
>
> What does not work, honestly:
>
> - On a GitHub project page the app lives at `/PeerPages/`, so a site asking
>   for `/style.css` escapes the Service Worker scope and 404s. A custom domain
>   removes this.
> - If both sides are behind symmetric NAT you need TURN, which is the single
>   exception to "no server" — optional, off by default, and the UI tells the
>   visitor when traffic is relayed.
> - Only Chromium is verified by the test suite. Socket.IO works in manual
>   testing but isn't covered.
> - No tags yet, so `go install` is the only install path today.
>
> Threat model: https://github.com/aliosmanmho/PeerPages/blob/main/docs/SECURITY.md
> Measurements (latency, throughput, bundle size, per phase):
> https://github.com/aliosmanmho/PeerPages/blob/main/docs/MEASUREMENTS.md
>
> Happy to answer anything about the protocol or the failure modes.

> **Note:** the comment above used to end with a line saying the CLI and
> the detailed docs were still in Turkish. Phase 8 made the whole
> repository English, so that line is gone; the web page now speaks both
> English and Turkish, with English as the default.

**The hard questions to expect, and the honest answers**

| Question | Answer |
|---|---|
| "Isn't a Nostr relay a server too?" | Yes, and we do not hide it: the relays carry the handshake, not the traffic. You can pass your own relay (`--relay`). The difference: a relay has no way to read the content, where ngrok does. |
| "GitHub Pages is a server as well." | It serves static files, not your traffic. You can host the page on your own domain too. |
| "If TURN is needed your claim collapses." | On symmetric NAT, yes, a relay is needed; that is why it is optional, off by default, and shown to the visitor on screen. Even TURN cannot see the content (DTLS). |
| "Anyone who knows the URL gets in." | True, and the README says "treat the URL like a password". `--secret` is the second layer. |
| "Has it been audited?" | No. NIP-44 and DTLS are not our own implementations, but the composition has not been audited; SECURITY.md says so plainly. |

---

## 3. r/selfhosted

**Title:** `PeerPages: share localhost over GitHub Pages — WebRTC data channel, Nostr signaling, no relay server`

> Self-hosting crowd will appreciate the specific thing here: there is no
> third-party server in the data path and no account anywhere.
>
> The visitor opens a static page on GitHub Pages, it installs a Service Worker
> and opens a WebRTC data channel to a single Go binary on my machine. The
> WebRTC handshake travels through public Nostr relays, encrypted end to end —
> a relay only learns that some public key received a blob. Traffic itself is
> DTLS between the two machines.
>
> - Single binary, `go install`, no daemon, no config file.
> - Allowlist only: the agent proxies to the targets you pass on the command
>   line; deriving a host from the request path is impossible by construction.
> - Optional TURN if your NAT needs it (own coturn or Cloudflare's free tier);
>   credentials never leave your machine.
> - `peerpages doctor` tells you up front whether your NAT will cooperate.
>
> Limits are in the README and there is a real threat model in docs/SECURITY.md
> (replay/clock-skew/rate-limit guards, body and stream ceilings, what the
> relays can see, what we deliberately traded away).
>
> It is a sharing tool, not a hosting product — the agent has to stay running
> and the page is not indexable.

---

## 4. r/webdev

**Title:** `Share your dev server with a client in one command — no signup, no tunnel provider`

> You run `peerpages serve --target http://localhost:3000`, you get a URL, you
> send it. Whoever opens it sees your dev server in their browser: relative
> links, CSS, images, fetch, downloads, SSE, and WebSockets (including Vite
> HMR) all work, because a Service Worker on the page intercepts every request
> and forwards it over a direct WebRTC connection to your machine.
>
> No account, no third-party proxy, nothing for the visitor to install. The
> static page is on GitHub Pages; the connection is browser-to-your-machine.
>
> Practical notes:
>
> - Vite HMR works with no configuration; editing a file updates the visitor's
>   page without a reload (there's an e2e test that asserts exactly that).
> - If your site uses root-absolute paths (`/style.css`) you'll want a custom
>   domain — on a GitHub project page those escape the Service Worker scope.
> - Keep the terminal open; the tunnel lives as long as the agent does.
>
> Chromium is verified by the test suite; Firefox and Safari should work but
> I haven't measured them. Feedback welcome, especially on browsers I can't
> test.

---

## 5. X / LinkedIn

Put the company account name in place of `<company-account>`.

**X (short):**

> Share your localhost over GitHub Pages — with no server in between.
>
> Static page + Service Worker + WebRTC data channel + Nostr for signaling.
> The visitor installs nothing, the traffic goes straight from their browser
> to your machine, and nobody in the middle can read it.
>
> Open source, MIT: github.com/aliosmanmho/PeerPages

**LinkedIn (a bit longer):**

> Most localhost tunnels work by terminating TLS on the provider's servers.
> That is fine until you are sharing a staging build with a client and realise
> a third party is decrypting it.
>
> PeerPages takes the server out of the path: a static page on GitHub Pages
> installs a Service Worker, opens a WebRTC data channel directly to a small Go
> agent on your machine, and uses public Nostr relays only to carry the
> encrypted handshake. No account, nothing for the visitor to install.
>
> It was built in seven phases, each with measurements and an honest list of
> what does not work — including a threat model and the trade-offs we
> deliberately accepted.
>
> MIT licensed: github.com/aliosmanmho/PeerPages
> (built with <company-account>)

---

## 6. The checklist before launching

In order, each one **actually tried**. The boxes ticked below were worked
through on 2026-10-28; what is left needs either your machine or your
decision.

- [x] The Pages site is live and the connect screen opens. (English by
      default, `?lang=tr` switches it, verified live.)
- [x] `go install github.com/aliosmanmho/PeerPages/agent/cmd/peerpages@latest` works **on
      a clean machine** (or at least in a clean `GOPATH`). This check is
      what found the binary-name bug: the old `…/agent@latest` path
      installed it as `agent`, so the quickstart's second command failed.
- [x] When the agent runs with no flags, the URL it prints **really
      opens** (`DefaultAppURL` is right). Checked with the installed
      binary: 3/4 relays, the printed address is the live one.
- [x] Every command in the README is copy-and-run: a single command with a
      typo decides the first comment on Show HN.
- [x] Every link in the README resolves (the images included). All five
      external links answer 200; the only unresolved relative link is
      `docs/img/demo.gif` inside the snippet above, which is waiting for
      the GIF.
- [x] The reporting channel in `docs/SECURITY.md` really works (is the
      Security Advisories tab enabled). It was **off**; private
      vulnerability reporting is enabled now, so the "preferred" channel
      in both SECURITY files is real.
- [ ] The GIF is in place and under 2 MB. **Yours to record** — see
      section 1. A scripted capture would be exactly the synthetic
      animation this document argues against.
- [x] The repository description (`gh repo edit --description`) and
      `homepageUrl` are filled in; the topics are added (`webrtc`,
      `nostr`, `tunnel`, `service-worker`, `golang`).
- [ ] The first-tag decision: launching without a tag means there is no
      download link, only `go install`. Do you want to push `v0.1.0`?
      (The "no tag yet" note in `docs/INSTALL.md` goes away then.)
      The tag text and the release build are ready; pushing it is the
      step that makes it public.
- [ ] Set aside 2–3 hours for launch day: not answering the questions that
      arrive in the first hour kills a Show HN post.

**Timing:** for Show HN, Tuesday to Thursday, 08:00–10:00 ET is
statistically the best window. Do not pile the Reddit posts onto the same
day; post them a day after HN so you can keep up with both sets of
questions.
