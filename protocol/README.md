# The PeerPages data channel protocol — the normative definition

This file is the **single source of truth** for the message format on the
`ctrl` and `data` channels. `testvectors.json` is the machine-readable
counterpart of this definition, and **both** the TypeScript
(`web/src/protocol/`) **and** the Go (`agent/internal/protocol/`) side are
tested by reading the same file.

> If you change the protocol: update this file, add vectors to
> `testvectors.json`, and get both sides green. A one-sided change is not
> accepted (`CONTRIBUTING.md`).

## The channels

| Channel | `ordered` | `reliable` | Contents |
|---|---|---|---|
| `ctrl` | yes | yes | UTF-8 JSON text, one message per line |
| `data` | yes | yes | binary frames (below) |

The reason for two channels is readability: a single channel with a 1-byte
type prefix was possible too, and two channels were chosen for the MVP
(BRIEF section 3).

The channels are opened by the **visitor** (the visitor is the offerer),
and the agent receives them through `OnDataChannel`. The direction swapped
in phase 2; the reasoning is in BRIEF section 8.

## The `data` frame

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        id (uint32, BE)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                       seq (uint32, BE)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                     payload (0 … 16384 bytes)                 |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

| Field | Type | Meaning |
|---|---|---|
| `id` | uint32, big-endian | the id of the request or the ws session; the same as in the `ctrl` `req`/`res`/`ws-open` |
| `seq` | uint32, big-endian | the order of this body chunk in this direction, starting **at 0** |
| `payload` | 0–16384 bytes | a body chunk |

- The header is **8 bytes** and the payload is at most **16384 bytes
  (16 KiB)**, so the largest frame is **16392 bytes**. The 16 KiB limit is
  there for browser data channel compatibility (BRIEF section 3).
- **An empty payload is valid** (an 8-byte frame). What says the body is
  over is not the payload but the `end` message on the `ctrl` channel.
- The same framing is used in **both directions**: for the request body
  and for the response body. Because `id` is unique per request and each
  side only receives data from the other direction, nothing gets mixed up.
  WebSocket sessions share **the same id space** (the visitor hands out
  the ids), so a collision is structurally impossible; `ws-open` is what
  makes an id a ws one rather than an HTTP one.
- `seq` is there for the receiver's order check. Because the channel is
  `ordered` it normally arrives incrementing; **an unexpected `seq` is a
  protocol error** and the request is `abort`ed (it is never silently
  reordered).

### Error codes (framing)

Both sides use the same codes; `testvectors.json` validates against these
names.

| Code | When |
|---|---|
| `short-frame` | the frame is shorter than 8 bytes (the header is incomplete) |
| `payload-too-large` | the payload is larger than 16384 bytes (on encode or decode) |

## The WebSocket payload: the frame sub-format (phase 6)

The WebSocket bridge **uses neither a new channel nor a new framing**: the
payload travels in the `data` frame above. The only difference is that the
payload of a frame belonging to a **ws id** carries a flag byte of its
own:

```
[1B flags][chunk <= 16383 bytes]

flags: bit0 (0x01) = 1 binary / 0 text message
       bit1 (0x02) = 1 this is the LAST chunk of the ws message
       every other bit has to be 0
```

The rules:

- A ws message is split across one or more frames; **only bit1** marks the
  message boundary. There is **no** per-message notice on the `ctrl` side
  (there is no ws counterpart of `end`).
- `seq` keeps incrementing across the session and is **not reset per
  message**; that way the existing order check (an unexpected `seq` is a
  protocol error) holds for ws exactly as it does for HTTP.
- The payload has to be **at least 1 byte** (the flags). An empty ws
  message is a single frame carrying only the flag byte.
- bit0 has to be the same on **every** chunk of a message.
- Because there is no ordering guarantee between `data` and `ctrl`, the
  first frames can arrive **before** `ws-open`; the receiver holds them in
  a short-lived buffer (the same mechanism that already exists for request
  bodies).

The reasoning behind this shape: backpressure, 16 KiB chunking and the
`seq` check come for free; binary data takes no base64 inflation; and bulk
ws traffic does not clog the `ctrl` channel (`ping`, `abort`).

### Error codes (ws frame)

| Code | When |
|---|---|
| `ws-empty-frame` | the payload of a ws frame is empty (no flag byte) |
| `ws-bad-flags` | the flag byte carries an undefined bit |
| `ws-payload-too-large` | the chunk exceeds 16383 bytes (on encode) |

When the first two are seen on the wire the session is closed with a
`ws-close` (`code: 1002`, a protocol error).

## The `ctrl` messages

UTF-8 JSON objects. The mandatory field is `t` (the type).

```jsonc
{ "t": "req",   "id": 17, "method": "GET", "path": "/api/items?x=1",
                "headers": { "accept": "..." }, "hasBody": false }
{ "t": "res",   "id": 17, "status": 200, "headers": { "content-type": "text/html" } }
{ "t": "end",   "id": 17, "frames": 6, "len": 90000 }   // the body is over
{ "t": "abort", "id": 17, "reason": "client-cancel" }
{ "t": "ping" }
{ "t": "pong" }
{ "t": "hello", "v": 1, "targets": ["default", "admin"], "auth": "none" }

// The WebSocket bridge (phase 6) — the payload travels on the `data`
// channel, these are the handshake
{ "t": "ws-open",  "id": 5, "path": "/socket", "protos": ["vite-hmr"] }
{ "t": "ws-ok",    "id": 5, "proto": "vite-hmr" }
{ "t": "ws-close", "id": 5, "code": 1000, "reason": "" }
```

### The field rules

| Field | Type | Rule |
|---|---|---|
| `id` | integer | `0 … 4294967295` (uint32). Fractional and negative are invalid. |
| `method` | string | must not be empty; an uppercase HTTP token only (`^[A-Z]+$`) |
| `path` | string | starts with `/`; an absolute URL and the `//host` form are invalid |
| `headers` | object | string→string; the keys are normalized to lowercase |
| `hasBody` | boolean | whether the request has a body |
| `status` | integer | `100 … 599` |
| `reason` | string | must not be empty |
| `v` | integer | only `1` for now |
| `targets` | array of strings | at least one item, all strings |
| `auth` | string | `"none"` or `"required"` |
| `frames` | integer | `end`: the number of body frames sent in this direction (`0 … 4294967295`) |
| `len` | integer | `end`: the total byte count of the body (`>= 0`) |
| `protos` | array of strings | `ws-open`: the requested subprotocols; **an empty array is valid**, and the items are RFC 6455 tokens |
| `proto` | string | `ws-ok`: the subprotocol the server chose; **an empty string is valid** (none chosen) |
| `code` | integer | `ws-close`: `1000 … 4999` |
| `reason` (`ws-close`) | string | **empty is valid**, at most 123 bytes (a WebSocket limit) |

`frames` and `len` have to agree: if one is 0 the other has to be 0 too.

The `path` validation is **the first layer of the security check**; the
second and deciding layer is `internal/target.Resolve` on the agent side
(BRIEF principle 1).

### Reserved headers

There is one header in `res.headers` that the agent adds itself:

| Header | Meaning |
|---|---|
| `x-peerpages-ws-shim: 1` | The agent runs with `--inject-ws-shim`; the page side should inject the WebSocket shim into this HTML response (phase 6). |

This header does **not** reach the visitor: the Service Worker strips it
from the response. Why a header rather than `hello`: `hello` is one way,
it refuses `v != 1`, and the canonical form has no optional fields — so
announcing a capability would have meant breaking the protocol version
(BRIEF section 8).

### Error codes (ctrl)

| Code | When |
|---|---|
| `bad-json` | the JSON could not be read, or the top level is not an object |
| `unknown-type` | `t` holds an unrecognised value (an empty string included) |
| `missing-field` | a field required for the type is missing |
| `bad-field` | the field is there but its type, range or format is invalid |

The order: `bad-json` → `missing-field` (when `t` is absent) → `bad-field`
(when `t` is not a string) → `unknown-type` → the type-specific
`missing-field` → `bad-field`.

### The canonical serialization

So the test vectors can be verified **byte for byte** in both languages, a
canonical form is defined:

1. No whitespace (`{"t":"end","id":17}`).
2. The key order is fixed per type:

   | Type | Order |
   |---|---|
   | `req` | `t, id, method, path, headers, hasBody` |
   | `res` | `t, id, status, headers` |
   | `end` | `t, id, frames, len` |
   | `abort` | `t, id, reason` |
   | `ping` / `pong` | `t` |
   | `hello` | `t, v, targets, auth` |
   | `ws-open` | `t, id, path, protos` |
   | `ws-ok` | `t, id, proto` |
   | `ws-close` | `t, id, code, reason` |

3. The `headers` keys are lowercase and in **lexicographic order**.
4. There are no optional fields: `hasBody` is always written (even when
   `false`), and `headers` is written as `{}` even when empty.
5. No HTML escaping (`<`, `>`, `&` stay as they are), and non-ASCII
   characters are not escaped with `\u`.

The tests check `canonical(parse(text)) == the canonical in the vector`;
that guarantees the two implementations behave the same both when parsing
and when producing.

## The flow

```
visitor                                                agent
    │                                                   │
    │◄──────────── ctrl: hello (v, targets, auth) ───────│  once the connection is up
    │                                                   │
    │─── ctrl: req  (id=1, GET /index.html) ────────────►│
    │                                                   │  → localhost:3000
    │◄── ctrl: res  (id=1, 200, content-type) ───────────│
    │◄── data: [id=1][seq=0][16 KiB] ────────────────────│
    │◄── data: [id=1][seq=1][…]      ────────────────────│
    │◄── ctrl: end  (id=1, frames=2, len=…) ─────────────│
    │                                                   │
    │─── ctrl: req  (id=2, POST /api, hasBody) ─────────►│
    │─── data: [id=2][seq=0][…] ────────────────────────►│
    │─── ctrl: end  (id=2, frames=1, len=…) ────────────►│
    │◄── ctrl: res  (id=2, 201, …) ──────────────────────│
    │◄── ctrl: end  (id=2, frames=0, len=0) ─────────────│
```

> **Why `end` carries counters — there is no ordering between the
> channels.** `ctrl` and `data` are two separate SCTP streams. Each is
> `ordered` within itself, but **there is no ordering guarantee between
> them**. Measured in phase 1 (pion loopback, a 90 KB response): six body
> frames arrived over `data` **after** the `end` message had arrived over
> `ctrl`.
>
> So the receiver cannot read `end` as "the body is over". The correct
> behaviour:
>
> 1. when `end` arrives, the `frames`/`len` expectation is recorded;
> 2. the stream is closed **once the expected frame count and byte count
>    are complete** (`data` keeps being read even if `end` arrived
>    earlier);
> 3. if the counts are already complete when `end` arrives, the stream
>    closes right away;
> 4. more frames or bytes than expected is a protocol error →
>    `abort` (`reason: "body-length-mismatch"`).
>
> This decision closed the open question in BRIEF section 8.

### The WebSocket flow (phase 6)

```
visitor                                                agent
    │─── ctrl: ws-open (id=5, /socket, protos) ────────►│
    │                                                   │  → ws://localhost:3000/socket
    │◄── ctrl: ws-ok   (id=5, proto) ────────────────────│
    │─── data: [id=5][seq=0][0x02 "hello"] ────────────►│  text, FIN
    │◄── data: [id=5][seq=0][0x00 "first half"] ─────────│  text, NOT FIN
    │◄── data: [id=5][seq=1][0x02 "second half"] ────────│  text, FIN
    │─── ctrl: ws-close (id=5, 1000, "") ──────────────►│
```

- Frames sent before `ws-ok` arrives are valid (there is no ordering
  between the two channels); the agent buffers them until it sees the
  `ws-open`.
- `ws-close` can come **from either direction** and ends the session in
  one move; the code and reason go straight into the other side's
  `onclose`. The codes the agent uses: 1002 a protocol error, 1008 policy
  (the path was refused or the bridge is off), 1009 the message is too
  large, 1011 the local server was unreachable, 1013 the stream budget is
  full.
- `abort` and `end` are **not used** for ws ids.

Beyond that:

- `abort` can come from either direction; the side that receives it stops
  the stream for that `id` and releases its resources.
- `ping`/`pong` are for liveness and latency measurement; they carry no
  `id`.
- `hello` goes once per connection, from the agent to the visitor.

## Backpressure

The sending side watches the data channel's `bufferedAmount`:

- `bufferedAmount > 1 MiB` → reading the body stops,
- `bufferedAmountLowThreshold = 256 KiB` and the `bufferedamountlow` event
  → reading resumes.

That keeps memory from blowing up and the channel from clogging on a 5 MB
download.
