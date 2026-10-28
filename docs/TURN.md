# TURN (for networks that cannot connect)

On most networks PeerPages needs no TURN: the agent and the visitor
connect to each other **directly** (with STUN's help). But on some
networks a direct connection cannot be made, and then the traffic has to
go over a **relay**. TURN is exactly the standard that does that.

## How to tell whether you need it

```bash
peerpages doctor
```

Look at the advice at the end of the output:

| What you see | What it means |
|---|---|
| `The NAT is friendly: no TURN needed.` | A direct connection can be made. Do nothing. |
| `The NAT is symmetric and no TURN was given` | If the other side is behind symmetric NAT too, it **cannot connect**. TURN is needed. |
| `STUN is unreachable` | Outbound UDP is blocked. TURN uses UDP too; try `turns:` (TCP/TLS). |

The symptom on the visitor side: the connect screen says **"The data
channels did not open"** while the signaling steps look complete. That is
not a software bug but a consequence of the network topology.

Once the tunnel is up, the **"Connection path"** row on the connect screen
says which route was chosen: `direct (local network)`,
`direct (through NAT)` or `through TURN (relay)`.

## Three ways

### 1. Your own TURN server (coturn)

The most control and the fewest dependencies. On a VPS:

```bash
sudo apt install coturn
```

`/etc/turnserver.conf`:

```
listening-port=3478
tls-listening-port=5349
fingerprint
lt-cred-mech
user=peerpages:A-VERY-LONG-PASSWORD
realm=example.com
# Your own public IP:
external-ip=203.0.113.10
# For your use only: set a quota.
user-quota=12
total-quota=1200
# Keep the relay from reaching your local network (SSRF-like abuse):
denied-peer-ip=10.0.0.0-10.255.255.255
denied-peer-ip=192.168.0.0-192.168.255.255
denied-peer-ip=172.16.0.0-172.31.255.255
```

Start the agent:

```bash
peerpages serve --target http://localhost:3000 \
  --turn turn:example.com:3478 --turn-user peerpages \
  --turn-pass A-VERY-LONG-PASSWORD
```

> **You do not have to put the password on the command line:** the
> `PEERPAGES_TURN_PASS` environment variable is read too. Command line
> arguments are visible to other users in `ps` output and in Task Manager.

### 2. Cloudflare Realtime TURN (short-lived credentials)

Cloudflare's TURN service is free up to 1 TB a month. The agent fetches
the credentials **itself** and refreshes them before they expire:

```bash
export PEERPAGES_CF_TURN_TOKEN=...   # the API token
peerpages serve --target http://localhost:3000 \
  --turn-cloudflare-key-id <KEY-ID>
```

What you need from the dashboard: the **TURN key id** and an **API token**
(Cloudflare Dashboard → Realtime → TURN).

> **No Worker is needed.** The README's first plan called for a small
> Worker under `cloudflare-turn/` to mint short-lived credentials. The
> Worker's only job was to hide the API token **from the browser**; since
> in PeerPages only the agent uses TURN and the token already sits on your
> machine, the server in between is unnecessary. That keeps the "zero
> servers" claim intact even with TURN.

> **Not tried:** this path has not been run against a real Cloudflare
> account. The request shape and the response parsing are verified by
> tests (`httptest`); the live verification is yours. If it does not work,
> share the output of
> `peerpages doctor --turn-cloudflare-key-id ...`.

### 3. Static credentials obtained by hand

You can pass the user and password you got from any TURN provider with
`--turn-user` / `--turn-pass`. If the credentials expire you have to
restart the agent when they do (on the Cloudflare path that is automatic).

## Verification

```bash
peerpages doctor --turn turn:example.com:3478 --turn-user peerpages --turn-pass ...
```

In the `TURN` section you should see `status: ok` and a **relay address**.
What is measured is not "is the server up" but **"does it give me a relay
address"**: wrong credentials, or an exhausted quota, produce a server
that looks up yet grants no allocation. If TURN was given and none of them
grants an allocation, `doctor` exits with a non-zero code.

A local trial (without a real TURN server):

```bash
# terminal 1
cd agent && go run . faketurn --port 0 --user u --pass p
# Take the address it prints: turn:127.0.0.1:XXXXX

# terminal 2 — make the direct path IMPOSSIBLE
cd agent && go run . serve --target http://localhost:3000 \
  --turn turn:127.0.0.1:XXXXX --turn-user u --turn-pass p --ice-relay-only
```

`--ice-relay-only` gathers relay candidates only, so if a connection can
be made it **must** have gone over TURN. It is a diagnostic flag and is
not needed in production (ICE prefers the direct path anyway, so no quota
is wasted).

## Security and cost

- **The credentials never leave the agent.** The visitor knows nothing
  about TURN: neither the address nor the user nor the password is sent to
  the page. One-sided TURN is enough, because the visitor sends packets
  straight to the agent's relay address. That way visitors you do not know
  cannot spend your quota (BRIEF section 6, principle 4: the visitor is
  treated as untrusted).
- **Even over TURN the traffic stays end-to-end encrypted.** A WebRTC data
  channel is encrypted with DTLS; the TURN server carries the bytes and
  **cannot see the content**. What it does see: the two ends' addresses
  and how much data flowed.
- **You still become dependent on a server.** That is the one exception to
  the project's "no server in between" claim — which is why TURN is
  **optional** and off by default. When TURN is in use the connect screen
  tells the visitor so.
- **Quota:** TURN carries all the traffic, so a 5 MB download takes 5 MB
  of the provider's quota. On your own coturn, set `user-quota`.
- The measured cost (on one machine, over loopback): a 5 MB download takes
  96 ms directly and 114 ms over TURN (about 16% slower). On a real
  network one round trip to the TURN server's location is added. Details:
  `MEASUREMENTS.md`.

## Limits

- `turns:` (TLS) addresses can be given to the agent and WebRTC uses them,
  but `peerpages doctor`'s allocation attempt skips TLS and tries UDP —
  the report says so honestly.
- Two-sided TURN (the visitor using a relay of their own) is not done. It
  is not needed: a one-sided relay works even when both sides are behind
  symmetric NAT.
