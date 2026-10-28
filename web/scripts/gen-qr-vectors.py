# -*- coding: utf-8 -*-
"""The generator of web/src/ui/qr-testvectors.json.

This script is a THIRD, INDEPENDENT implementation: our encoder does not
produce the vectors, it is verified AGAINST them. The same arrangement as
the protocol vectors (protocol/gen-signaling-vectors.py): testing against
something we produced ourselves proves nothing.

Usage:

    pip install qrcode
    python -I scripts/gen-qr-vectors.py

Output: web/src/ui/qr-testvectors.json (deterministic — the same inputs
produce the same file).

The scope is the same as qr.ts: byte mode, error correction level M,
versions 1-10.

Why the mask is forced: the `qrcode` package does not expose which mask it
chose automatically. A byte-for-byte comparison only means something with
a fixed mask. The automatic choice (the penalty rules) is verified
separately: for the same text the matrix the library produced
AUTOMATICALLY is written into the vector too and compared against our own
automatic choice.
"""
import collections
import io
import json
import os

import qrcode
from qrcode.constants import ERROR_CORRECT_M
from qrcode.util import MODE_8BIT_BYTE, QRData

OD = collections.OrderedDict

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "src", "ui", "qr-testvectors.json")

# PeerPages's real URL form: a 63-character npub plus a target.
NPUB = "npub1yl4dshpq3h2kqmcx8dz3xhdmgpkcrxu6l9rdj5vxk9chpsd0pdnn4ldqqq"
VISITOR_URL = "https://user.github.io/PeerPages/#" + NPUB
VISITOR_URL_TARGET = VISITOR_URL + "/admin"


def matrix_rows(m):
    """Turns the matrix into one "01" string per row (to keep the file compact)."""
    return ["".join("1" if cell else "0" for cell in row) for row in m]


def build(text, mask=None):
    q = qrcode.QRCode(error_correction=ERROR_CORRECT_M, border=0, mask_pattern=mask)
    # Byte mode is forced EXPLICITLY. The library's automatic mode choice
    # turns text that is all uppercase and digits into ALPHANUMERIC mode
    # ("PEERPAGES" was encoded that way), and then the vectors fall
    # outside our scope. The scope is deliberately a single mode (see
    # qr.ts).
    q.add_data(QRData(text.encode("utf-8"), mode=MODE_8BIT_BYTE), optimize=0)
    q.make(fit=True)
    return q


def case(name, text, mask):
    q = build(text, mask)
    return OD([
        ("name", name),
        ("text", text),
        ("mask", mask),
        ("version", q.version),
        ("size", 4 * q.version + 17),
        ("modules", matrix_rows(q.get_matrix())),
    ])


def auto_case(name, text):
    """Without forcing a mask: the library's own penalty choice."""
    q = build(text, None)
    return OD([
        ("name", name),
        ("text", text),
        ("version", q.version),
        ("size", 4 * q.version + 17),
        ("modules", matrix_rows(q.get_matrix())),
    ])


doc = OD()
doc["note"] = (
    "The test vectors of the PeerPages QR encoder. web/src/ui/qr.ts is "
    "verified against THIS file. The vectors were produced with a third, "
    "independent implementation (python qrcode). The scope: byte mode, "
    "level M, versions 1-10."
)
doc["errorCorrection"] = "M"
doc["mode"] = "byte"
doc["maxVersion"] = 10

# The forced-mask cases: the whole pipeline (the bit stream, RS,
# interleaving, placement, the format and version information) is verified
# byte for byte.
forced = []
forced.append(case("short-ascii", "PEERPAGES", 0))
forced.append(case("single-char", "a", 3))
for mask in range(8):
    forced.append(case("mask-%d" % mask, "PeerPages QR", mask))
# Push the version boundaries: the real URL with an npub, and with a
# target.
forced.append(case("visitor-url", VISITOR_URL, 2))
forced.append(case("visitor-url-with-target", VISITOR_URL_TARGET, 5))
# UTF-8 multi-byte characters: does byte mode count them correctly.
forced.append(case("utf8", u"cok güzel: çğüşİ", 1))
# Version 7+: are the version information blocks written.
forced.append(case("version-7-and-above", "x" * 130, 4))
# Version 10: the largest supported version, with two block groups.
forced.append(case("version-10", "y" * 213, 6))
doc["forced"] = forced

# The automatic mask choice: whether the penalty rules are right.
doc["auto"] = [
    auto_case("auto-short", "PEERPAGES"),
    auto_case("auto-url", VISITOR_URL),
    auto_case("auto-url-with-target", VISITOR_URL_TARGET),
    auto_case("auto-utf8", u"çğüşİö"),
    auto_case("auto-long", "z" * 100),
]

# The capacity limit: 3 of the 216 data codewords go to the mode, the
# count and the terminator, so byte mode holds at most 213 bytes.
doc["limits"] = OD([
    ("maxBytes", 213),
    ("note", "version 10, level M, byte mode"),
])

out = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
io.open(OUT, "w", encoding="utf-8", newline="\n").write(out)
print("written: %d bytes | forced=%d auto=%d" % (
    len(out.encode("utf-8")), len(doc["forced"]), len(doc["auto"])))
