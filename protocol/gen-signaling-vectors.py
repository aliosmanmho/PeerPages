# -*- coding: utf-8 -*-
"""The generator of protocol/signaling-testvectors.json.

This script is a THIRD, INDEPENDENT implementation: neither Go nor TS
produces the vectors, both are verified against this file (the same
arrangement as in phase 1). That way the two implementations cannot make
the same mistake together.

Usage (from the protocol/ directory or anywhere else):

    pip install cryptography
    python gen-signaling-vectors.py

Output: signaling-testvectors.json next to this file.

The crypto: HKDF-SHA256 + ChaCha20-Poly1305 (the python `cryptography`
package). The specification: protocol/signaling.md
"""
import base64
import collections
import io
import json
import os

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

OD = collections.OrderedDict

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "signaling-testvectors.json")

VERSION = 1
SID = "0123456789abcdef"

SALT = b"peerpages-secret-v1"
INFO = b"signaling"
AAD = b"peerpages-signaling-v1"
PREFIX = "pps1."

MAX_SDP = 65536
MAX_CAND = 1024
MAX_MID = 32
MAX_REASON = 64

MAX_TARGET = 32

ORDER = {
    "offer": ["v", "t", "sid", "sdp", "target"],
    "answer": ["v", "t", "sid", "sdp"],
    "candidate": ["v", "t", "sid", "cand", "mid"],
    "bye": ["v", "t", "sid", "reason"],
}


def canonical(msg):
    # An optional field (today only offer.target) is NOT WRITTEN at all
    # when absent: the canonical form of a target-less offer stays the same
    # as it was before the field existed.
    out = OD()
    for k in ORDER[msg["t"]]:
        if k in msg:
            out[k] = msg[k]
    return json.dumps(out, separators=(",", ":"), ensure_ascii=False)


def derive(secret):
    return HKDF(
        algorithm=hashes.SHA256(), length=32, salt=SALT, info=INFO
    ).derive(secret.encode("utf-8"))


def seal(secret, nonce_hex, plaintext):
    key = derive(secret)
    nonce = bytes.fromhex(nonce_hex)
    ct = ChaCha20Poly1305(key).encrypt(nonce, plaintext.encode("utf-8"), AAD)
    blob = nonce + ct
    b64 = base64.urlsafe_b64encode(blob).decode("ascii").rstrip("=")
    return PREFIX + b64


def ok(name, msg):
    return OD([("name", name), ("text", canonical(msg)), ("canonical", canonical(msg))])


def bad(name, text, err):
    return OD([("name", name), ("text", text), ("error", err)])


def j(s):
    """Turns single quotes into double quotes; it makes writing JSON text easier."""
    return s.replace("'", '"')


SDP_OFFER = "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n"
SDP_ANSWER = "v=0\r\no=- 9 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=setup:active\r\n"
CAND = "candidate:842163049 1 udp 1677729535 91.93.232.50 18212 typ srflx raddr 0.0.0.0 rport 0 generation 0"

doc = OD()
doc["version"] = VERSION
doc["note"] = (
    "The shared test vectors of the PeerPages signaling protocol. Both "
    "web/src/nostr (TS) and agent/internal/signaling (Go) read THIS file. "
    "The vectors were produced with a third, independent implementation "
    "(python). Details: protocol/signaling.md"
)
doc["limits"] = OD([
    ("sidLength", 16),
    ("maxSDP", MAX_SDP),
    ("maxCandidate", MAX_CAND),
    ("maxMid", MAX_MID),
    ("maxReason", MAX_REASON),
    ("maxTargetName", MAX_TARGET),
])
doc["targetNamePattern"] = "^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$"
doc["errors"] = ["bad-secret", "bad-json", "unknown-type", "missing-field", "bad-field"]
doc["secret"] = OD([
    ("prefix", PREFIX),
    ("salt", SALT.decode()),
    ("info", INFO.decode()),
    ("aad", AAD.decode()),
    ("kdf", "HKDF-SHA256"),
    ("aead", "ChaCha20-Poly1305"),
    ("nonceBytes", 12),
    ("base64", "url, dolgu yok"),
])

# --- the canonical messages ---
msgs = OD()
msgs["valid"] = [
    ok("offer", {"v": 1, "t": "offer", "sid": SID, "sdp": SDP_OFFER}),
    ok("answer", {"v": 1, "t": "answer", "sid": SID, "sdp": SDP_ANSWER}),
    ok("candidate", {"v": 1, "t": "candidate", "sid": SID, "cand": CAND, "mid": "0"}),
    ok("bye", {"v": 1, "t": "bye", "sid": SID, "reason": "visitor-left"}),
    ok("bye-agent", {"v": 1, "t": "bye", "sid": "fedcba9876543210", "reason": "agent-shutdown"}),
    # The escaping rules: an SDP already carries \r\n; non-ASCII and HTML
    # characters must not be escaped either.
    ok("reason-utf8", {"v": 1, "t": "bye", "sid": SID, "reason": "cok uzun surdu: cgu"}),
    ok("reason-html", {"v": 1, "t": "bye", "sid": SID, "reason": "a<b>&c"}),
    ok("sid-tum-hex", {"v": 1, "t": "bye", "sid": "00000000000000ff", "reason": "x"}),
    ok("mid-bir", {"v": 1, "t": "candidate", "sid": SID, "cand": CAND, "mid": "1"}),
    # Phase 3: the optional target name in an offer.
    ok("offer-hedefli", {"v": 1, "t": "offer", "sid": SID, "sdp": SDP_OFFER, "target": "admin"}),
    ok("offer-hedef-tek-karakter",
       {"v": 1, "t": "offer", "sid": SID, "sdp": SDP_OFFER, "target": "a"}),
    ok("offer-hedef-tam-sinir",
       {"v": 1, "t": "offer", "sid": SID, "sdp": SDP_OFFER, "target": "a" * MAX_TARGET}),
    ok("offer-hedef-tireli",
       {"v": 1, "t": "offer", "sid": SID, "sdp": SDP_OFFER, "target": "web-2_eski"}),
    ok("bye-unknown-target", {"v": 1, "t": "bye", "sid": SID, "reason": "unknown-target"}),
]
# Non-ASCII characters put in explicitly (escaped, so this source file
# stays ASCII).
msgs["valid"][5]["text"] = canonical(
    {"v": 1, "t": "bye", "sid": SID, "reason": "cok uzun s\u00fcrd\u00fc: \u00e7\u011f\u00fc"})
msgs["valid"][5]["canonical"] = msgs["valid"][5]["text"]

msgs["invalid"] = [
    bad("bos-metin", "", "bad-json"),
    bad("bozuk-json", j("{'v':1,'t':'bye'"), "bad-json"),
    bad("json-dizi", j("[1,2]"), "bad-json"),
    bad("json-sayi", "7", "bad-json"),
    bad("json-null", "null", "bad-json"),

    bad("t-yok", j("{'v':1,'sid':'" + SID + "'}"), "missing-field"),
    bad("t-sayi", j("{'v':1,'t':5,'sid':'" + SID + "'}"), "bad-field"),
    bad("t-bos", j("{'v':1,'t':'','sid':'" + SID + "'}"), "unknown-type"),
    bad("t-bilinmeyen", j("{'v':1,'t':'zipzip','sid':'" + SID + "'}"), "unknown-type"),

    bad("v-yok", j("{'t':'bye','sid':'" + SID + "','reason':'x'}"), "missing-field"),
    bad("v-yanlis", j("{'v':2,'t':'bye','sid':'" + SID + "','reason':'x'}"), "bad-field"),
    bad("v-metin", j("{'v':'1','t':'bye','sid':'" + SID + "','reason':'x'}"), "bad-field"),

    bad("sid-yok", j("{'v':1,'t':'bye','reason':'x'}"), "missing-field"),
    bad("sid-kisa", j("{'v':1,'t':'bye','sid':'abc','reason':'x'}"), "bad-field"),
    bad("sid-uzun", j("{'v':1,'t':'bye','sid':'" + "a" * 17 + "','reason':'x'}"), "bad-field"),
    bad("sid-buyuk-harf", j("{'v':1,'t':'bye','sid':'0123456789ABCDEF','reason':'x'}"), "bad-field"),
    bad("sid-hex-degil", j("{'v':1,'t':'bye','sid':'0123456789abcdeg','reason':'x'}"), "bad-field"),
    bad("sid-sayi", j("{'v':1,'t':'bye','sid':123,'reason':'x'}"), "bad-field"),

    bad("offer-sdp-yok", j("{'v':1,'t':'offer','sid':'" + SID + "'}"), "missing-field"),
    bad("offer-sdp-bos", j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':''}"), "bad-field"),
    bad("offer-sdp-sayi", j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':1}"), "bad-field"),
    bad("answer-sdp-yok", j("{'v':1,'t':'answer','sid':'" + SID + "'}"), "missing-field"),

    bad("candidate-cand-yok", j("{'v':1,'t':'candidate','sid':'" + SID + "','mid':'0'}"), "missing-field"),
    bad("candidate-cand-bos", j("{'v':1,'t':'candidate','sid':'" + SID + "','cand':'','mid':'0'}"), "bad-field"),
    bad("candidate-mid-yok", j("{'v':1,'t':'candidate','sid':'" + SID + "','cand':'candidate:1'}"), "missing-field"),
    bad("candidate-mid-bos", j("{'v':1,'t':'candidate','sid':'" + SID + "','cand':'candidate:1','mid':''}"), "bad-field"),
    bad("candidate-mid-uzun",
        j("{'v':1,'t':'candidate','sid':'" + SID + "','cand':'candidate:1','mid':'" + "m" * 33 + "'}"),
        "bad-field"),

    # Phase 3: a target name that is present but invalid. (The field being
    # absent entirely is valid.)
    bad("offer-hedef-bos",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':''}"), "bad-field"),
    bad("offer-hedef-sayi",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':5}"), "bad-field"),
    bad("offer-hedef-null",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':null}"), "bad-field"),
    bad("offer-hedef-uzun",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':'"
          + "a" * (MAX_TARGET + 1) + "'}"), "bad-field"),
    bad("offer-hedef-tire-ile-basliyor",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':'-admin'}"), "bad-field"),
    bad("offer-hedef-slash",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':'a/b'}"), "bad-field"),
    bad("offer-hedef-nokta",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':'a.b'}"), "bad-field"),
    bad("offer-hedef-bosluk",
        j("{'v':1,'t':'offer','sid':'" + SID + "','sdp':'v=0','target':'a b'}"), "bad-field"),

    bad("bye-reason-yok", j("{'v':1,'t':'bye','sid':'" + SID + "'}"), "missing-field"),
    bad("bye-reason-bos", j("{'v':1,'t':'bye','sid':'" + SID + "','reason':''}"), "bad-field"),
    bad("bye-reason-uzun",
        j("{'v':1,'t':'bye','sid':'" + SID + "','reason':'" + "r" * 65 + "'}"), "bad-field"),
]

# The boundary cases: a length exactly at the limit is ACCEPTED, one more
# is REFUSED.
msgs["generated"] = [
    OD([("name", "sdp-tam-sinir"), ("kind", "offer-sdp"), ("len", MAX_SDP), ("accept", True)]),
    OD([("name", "sdp-sinir-asimi"), ("kind", "offer-sdp"), ("len", MAX_SDP + 1), ("accept", False)]),
    OD([("name", "cand-tam-sinir"), ("kind", "candidate-cand"), ("len", MAX_CAND), ("accept", True)]),
    OD([("name", "cand-sinir-asimi"), ("kind", "candidate-cand"), ("len", MAX_CAND + 1), ("accept", False)]),
    OD([("name", "reason-tam-sinir"), ("kind", "bye-reason"), ("len", MAX_REASON), ("accept", True)]),
    OD([("name", "reason-sinir-asimi"), ("kind", "bye-reason"), ("len", MAX_REASON + 1), ("accept", False)]),
    OD([("name", "mid-tam-sinir"), ("kind", "candidate-mid"), ("len", MAX_MID), ("accept", True)]),
    OD([("name", "mid-sinir-asimi"), ("kind", "candidate-mid"), ("len", MAX_MID + 1), ("accept", False)]),
]
doc["messages"] = msgs

# --- the secret layer ---
SECRET = "kedi-balik-7"
NONCE1 = "000102030405060708090a0b"
NONCE2 = "0b0a090807060504030201ff"

bye_text = canonical({"v": 1, "t": "bye", "sid": SID, "reason": "visitor-left"})
offer_text = canonical({"v": 1, "t": "offer", "sid": SID, "sdp": SDP_OFFER})

sealed = OD()
sealed["key"] = OD([
    ("secret", SECRET),
    ("keyHex", derive(SECRET).hex()),
])
sealed["valid"] = [
    OD([("name", "bye-sirli"), ("secret", SECRET), ("nonceHex", NONCE1),
        ("plaintext", bye_text), ("payload", seal(SECRET, NONCE1, bye_text))]),
    OD([("name", "offer-sirli"), ("secret", SECRET), ("nonceHex", NONCE2),
        ("plaintext", offer_text), ("payload", seal(SECRET, NONCE2, offer_text))]),
    OD([("name", "utf8-sirli"), ("secret", "gizli-\u015fifre-\u00e7\u011f"), ("nonceHex", NONCE1),
        ("plaintext", bye_text), ("payload", seal("gizli-\u015fifre-\u00e7\u011f", NONCE1, bye_text))]),
]

_good = seal(SECRET, NONCE1, bye_text)
_body = _good[len(PREFIX):]
sealed["invalid"] = [
    OD([("name", "yanlis-sir"), ("secret", "baska-sir"), ("payload", _good), ("error", "bad-secret")]),
    OD([("name", "bozuk-base64"), ("secret", SECRET), ("payload", PREFIX + "!!!not-base64!!!"),
        ("error", "bad-secret")]),
    OD([("name", "cok-kisa"), ("secret", SECRET), ("payload", PREFIX + base64.urlsafe_b64encode(b"kisa").decode().rstrip("=")),
        ("error", "bad-secret")]),
    # We change the last character to break the AEAD tag.
    OD([("name", "etiket-bozuk"), ("secret", SECRET),
        ("payload", PREFIX + _body[:-1] + ("A" if _body[-1] != "A" else "B")),
        ("error", "bad-secret")]),
    OD([("name", "bos-govde"), ("secret", SECRET), ("payload", PREFIX), ("error", "bad-secret")]),
]
# Plain JSON arriving where a secret is expected has to be refused too.
sealed["plainWhenSecretExpected"] = OD([
    ("secret", SECRET), ("payload", bye_text), ("error", "bad-secret"),
])
# A pps1 payload arriving with no secret has to be refused too.
sealed["sealedWhenNoSecret"] = OD([
    ("payload", _good), ("error", "bad-secret"),
])
doc["sealed"] = sealed

out = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
io.open(OUT, "w", encoding="utf-8", newline="\n").write(out)
print("written: %d bytes | valid=%d invalid=%d boundary=%d | sealed valid=%d invalid=%d" % (
    len(out.encode("utf-8")), len(msgs["valid"]), len(msgs["invalid"]), len(msgs["generated"]),
    len(sealed["valid"]), len(sealed["invalid"])))
