# The PeerPages signaling protocol — the normative definition

This file is the **single source of truth** for the signaling messages
carried over Nostr. `signaling-testvectors.json` is the machine-readable
counterpart of this definition, and **both** the TypeScript
(`web/src/nostr/`) **and** the Go (`agent/internal/signaling/`) side are
tested by reading the same file.

The data channel protocol lives in a separate file:
[`README.md`](README.md).

> If you change it: update this file, add vectors, and get both sides
> green. A one-sided change is not accepted (`../CONTRIBUTING.md`).

## The transport: a Nostr event

| Field | Value |
|---|---|
| `kind` | **25050** — in the ephemeral range (20000–29999), so relays do not store it |
| `tags` | `[["p", "<recipient pubkey hex>"]]` |
| `content` | the *payload* (below) encrypted with **NIP-44** |
| `pubkey` | the sender's pubkey (on the visitor side a new, ephemeral one on every visit) |

Relays only see "a pubkey received something"; the content is closed to
them (`../docs/BRIEF.md` section 6, principle 2).

### Who is who

- **The agent**: a long-lived identity. peer-id = the **full npub**, after
  the `#` in the URL.
- **The visitor**: generates a **new ephemeral keypair** on every visit.

### The flow

```
visitor                          relays                          agent
    │                                │                              │
    │  offer     (#p = agent npub) ─►│──────────────────────────────►│
    │                                │                              │ Session(answerer)
    │◄───────────────────────────────│◄── answer    (#p = visitor) ──│
    │  candidate (#p = agent)     ──►│──────────────────────────────►│
    │◄───────────────────────────────│◄── candidate (#p = visitor) ──│
    │                                │                              │
    │           … WebRTC comes up, signaling ends …                  │
    │                                │                              │
    │  bye       (#p = agent)     ──►│──────────────────────────────►│
```

**The visitor publishes the offer.** The agent cannot know the visitor's
ephemeral pubkey in advance — so it cannot send the first message (it
would not know who to encrypt to). The decision and its reasoning are in
`../docs/BRIEF.md` section 8.

## The payload

The plain text put *inside* NIP-44. It has two forms:

| Form | When |
|---|---|
| canonical JSON (below) | there is no shared secret |
| `pps1.<base64url>` | there is a shared secret (`--secret`) |

The receiver tells which one it is by whether the payload starts with
`pps1.`. If the agent was started with `--secret` it **refuses an unsealed
payload**; if it was started without one it cannot open a `pps1.` payload
and refuses it.

## The messages

```jsonc
{ "v": 1, "t": "offer",     "sid": "0123456789abcdef", "sdp": "v=0\r\n…" }
{ "v": 1, "t": "offer",     "sid": "0123456789abcdef", "sdp": "v=0\r\n…", "target": "admin" }
{ "v": 1, "t": "answer",    "sid": "0123456789abcdef", "sdp": "v=0\r\n…" }
{ "v": 1, "t": "candidate", "sid": "0123456789abcdef", "cand": "candidate:…", "mid": "0" }
{ "v": 1, "t": "bye",       "sid": "0123456789abcdef", "reason": "visitor-left" }
```

### The field rules

| Field | Type | Rule |
|---|---|---|
| `v` | integer | only `1` for now |
| `t` | string | `offer` / `answer` / `candidate` / `bye` |
| `sid` | string | **exactly 16 lowercase hex** characters (64 bits) |
| `sdp` | string | must not be empty, at most 65536 bytes |
| `target` | string | **on `offer` only, and optional**; `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$` (1–32 characters) |
| `cand` | string | must not be empty, at most 1024 bytes |
| `mid` | string | must not be empty, at most 32 bytes (`sdpMid`) |
| `reason` | string | must not be empty, at most 64 bytes |

### Choosing the target (`target`)

One agent can share several local projects
(`--target default=http://localhost:3000 --target admin=http://localhost:4000`).
The visitor says which one they want **in the URL** (`#npub1…/admin`), and
that name rides in the `offer`.

| Case | Behaviour |
|---|---|
| no `target` | the agent uses `default`; with none, the alphabetically first target |
| `target` is a defined name | that target is used |
| `target` is an undefined name | the agent answers `bye` + `reason: "unknown-target"` and **no session is built at all** |

The target is **fixed per session**: it does not change once the tunnel is
up, and changing it means a new connection (a new URL). That is why the
field lives in the `offer` rather than in the data channel protocol's
`req` message — phase 1's framing and canonical key order never changed.
The list of target names is also announced after the tunnel opens, in the
`targets` field of the ctrl `hello` message (see [`README.md`](README.md)).

`target` is **meaningless** on the other message types and is not read
(the parser ignores fields it does not recognise in general).

Why `sid` exists: several visitors can reach one agent at the same time,
and which session a `candidate` message belongs to has to be known. The
visitor generates it and the agent mirrors it back.

**End-of-candidates is not carried.** The browser calls
`onicecandidate` one more time with `null`; we do not send that, we simply
stop sending. That is why `cand` cannot be empty.

### The error codes

**The same** set of codes as the data channel protocol
(`README.md`):

| Code | When |
|---|---|
| `bad-json` | the JSON could not be read, or the top level is not an object |
| `unknown-type` | `t` holds an unrecognised value (an empty string included) |
| `missing-field` | a field required for the type is missing |
| `bad-field` | the field is there but its type, range or format is invalid |
| `bad-secret` | a `pps1.` payload could not be opened (a wrong or missing secret), or plain JSON arrived where a secret was expected |

The order: `bad-secret` → `bad-json` → `missing-field` (when `t` is
absent) → `bad-field` (when `t` is not a string) → `unknown-type` → the
type-specific checks.

### The canonical serialization

So the vectors can be verified **byte for byte** in both languages:

1. No whitespace.
2. The key order is fixed per type:

   | Type | Order |
   |---|---|
   | `offer` | `v, t, sid, sdp` plus `, target` when there is one |
   | `answer` | `v, t, sid, sdp` |
   | `candidate` | `v, t, sid, cand, mid` |
   | `bye` | `v, t, sid, reason` |

3. An absent optional field is **not written at all** — not as an empty
   string and not as `null`. Today that holds for `offer.target` only; the
   canonical form of a target-less `offer` is **exactly** what it was
   before this field existed, so the old vectors stay valid as they are.
4. No HTML escaping, and non-ASCII characters are not escaped with `\u`.
   Control characters are escaped the same way `JSON.stringify` does
   (`\n`, `\r`, `\t`, `\b`, `\f`, everything else `\u00xx`). Because an
   SDP contains `\r\n`, that rule applies to every message in practice.

## The shared secret layer (`--secret`)

The secret is an extra layer **beneath NIP-44**. Its point: someone who
knows the peer-id (the npub) but not the secret cannot send the agent a
valid offer.

```
key     = HKDF-SHA256(
              ikm  = UTF-8(secret),
              salt = UTF-8("peerpages-secret-v1"),
              info = UTF-8("signaling"),
              len  = 32)

nonce   = 12 random bytes
ct      = ChaCha20-Poly1305_Seal(
              key = key,
              nonce = nonce,
              plaintext = the canonical JSON,
              aad = UTF-8("peerpages-signaling-v1"))

payload = "pps1." + base64url_nopad(nonce || ct)
```

- `base64url_nopad`: the RFC 4648 section 5 alphabet (`-` and `_`), with
  **no padding**.
- `ct` includes the AEAD tag (16 bytes).
- Opening it is the reverse; if the tag cannot be verified it is
  `bad-secret`.

**What the secret does not protect:** relays still see "this npub received
something", so neither that the agent is online nor when it received
traffic is hidden. The secret only guards *the right to connect*.

## The security rules (agent side)

The visitor is untrusted (`../docs/BRIEF.md` section 6, principle 4):

| Rule | Why |
|---|---|
| Refuse a `created_at` outside ±5 min | the replay of old events |
| Remember the event ids seen for a while and refuse repeats | replay |
| A rate limit per visitor pubkey | spam and resource exhaustion |
| A concurrent session limit | resource exhaustion |
| Refuse a `candidate` for an unknown `sid` | state pollution |
| Size limits on `sdp` and `cand` | memory exhaustion |
| Shorten pubkeys in the logs | an unnecessary identity trail |

Enforcing these rules happens in `agent/internal/signaling` and in the
agent flow (phase 2.5); this file only defines **what** they are.
