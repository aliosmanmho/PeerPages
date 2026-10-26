# -*- coding: utf-8 -*-
"""web/src/ui/qr-testvectors.json ureticisi.

Bu betik UCUNCU bir BAGIMSIZ uygulamadir: vektorleri bizim kodlayicimiz
uretmez, ona KARSI dogrulanir. Protokol vektorlerindeki ayni duzen
(protocol/gen-signaling-vectors.py): kendi urettigimiz seye karsi test etmek
bir sey kanitlamaz.

Kullanim:

    pip install qrcode
    python -I scripts/gen-qr-vectors.py

Cikti: web/src/ui/qr-testvectors.json (deterministik — ayni girdiler ayni
dosyayi uretir).

Kapsam, qr.ts ile ayni: byte modu, hata duzeltme seviyesi M, surum 1-10.

Maske neden zorlaniyor: `qrcode` paketi otomatik secimde hangi maskeyi
sectigini dismarivermiyor. Bayt bayt karsilastirma ancak maske sabitken
anlamlidir. Otomatik secimin (penalti kurallarinin) dogrulugu ayrica
dogrulanir: ayni metin icin kutuphanenin OTOMATIK urettigi matris de
vektore yaziliyor ve bizim otomatik secimimizle karsilastiriliyor.
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

# PeerPages'in gercek URL bicimi: 63 karakterlik npub + hedef.
NPUB = "npub1yl4dshpq3h2kqmcx8dz3xhdmgpkcrxu6l9rdj5vxk9chpsd0pdnn4ldqqq"
VISITOR_URL = "https://kullanici.github.io/PeerPages/#" + NPUB
VISITOR_URL_TARGET = VISITOR_URL + "/admin"


def matrix_rows(m):
    """Matrisi satir basina "01" dizisine cevirir (dosya kompakt kalsin)."""
    return ["".join("1" if cell else "0" for cell in row) for row in m]


def build(text, mask=None):
    q = qrcode.QRCode(error_correction=ERROR_CORRECT_M, border=0, mask_pattern=mask)
    # Byte modu ACIKCA zorlaniyor. Kutuphanenin otomatik mod secimi, tamami
    # buyuk harf/rakam olan metinleri ALFANUMERIK moda cevirir ("PEERPAGES"
    # boyle kodlanmisti) ve o zaman vektorler bizim kapsamimizin disina
    # cikar. Kapsam bilincli olarak tek mod (bkz. qr.ts).
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
    """Maske zorlanmadan: kutuphanenin kendi penalti secimi."""
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
    "PeerPages QR kodlayicisinin test vektorleri. web/src/ui/qr.ts BU "
    "dosyaya karsi dogrulanir. Vektorler ucuncu bir bagimsiz uygulama "
    "(python qrcode) ile uretildi. Kapsam: byte modu, seviye M, surum 1-10."
)
doc["errorCorrection"] = "M"
doc["mode"] = "byte"
doc["maxVersion"] = 10

# Maske zorlanmis vakalar: boru hattinin tamami (bit akisi, RS, serpistirme,
# yerlestirme, bicim/surum bilgisi) bayt bayt dogrulanir.
forced = []
forced.append(case("kisa-ascii", "PEERPAGES", 0))
forced.append(case("tek-karakter", "a", 3))
for mask in range(8):
    forced.append(case("maske-%d" % mask, "PeerPages QR", mask))
# Surum sinirlarini zorla: npub'li gercek URL ve hedefli hali.
forced.append(case("ziyaretci-url", VISITOR_URL, 2))
forced.append(case("ziyaretci-url-hedefli", VISITOR_URL_TARGET, 5))
# UTF-8 cok baytli karakterler: byte modu dogru sayiyor mu.
forced.append(case("utf8", u"cok güzel: çğüşİ", 1))
# Surum 7+: surum bilgisi bloklari yaziliyor mu.
forced.append(case("surum-7-uzeri", "x" * 130, 4))
# Surum 10: en buyuk desteklenen surum, cift blok grubu.
forced.append(case("surum-10", "y" * 213, 6))
doc["forced"] = forced

# Otomatik maske secimi: penalti kurallarinin dogrulugu.
doc["auto"] = [
    auto_case("auto-kisa", "PEERPAGES"),
    auto_case("auto-url", VISITOR_URL),
    auto_case("auto-url-hedefli", VISITOR_URL_TARGET),
    auto_case("auto-utf8", u"çğüşİö"),
    auto_case("auto-uzun", "z" * 100),
]

# Kapasite siniri: 216 veri kod sozcugunun 3'u mod+sayac+sonlandiriciya
# gidiyor, yani byte modunda en fazla 213 bayt.
doc["limits"] = OD([
    ("maxBytes", 213),
    ("note", "surum 10, seviye M, byte modu"),
])

out = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
io.open(OUT, "w", encoding="utf-8", newline="\n").write(out)
print("yazildi: %d bayt | zorlanmis=%d otomatik=%d" % (
    len(out.encode("utf-8")), len(doc["forced"]), len(doc["auto"])))
