package identity_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gonostr "github.com/nbd-wtf/go-nostr"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
)

func TestGenerateProducesValidPair(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !gonostr.IsValid32ByteHex(id.SecretKey) {
		t.Errorf("gizli anahtar hex degil: %q", id.SecretKey)
	}
	if !gonostr.IsValid32ByteHex(id.PublicKey) {
		t.Errorf("pubkey hex degil: %q", id.PublicKey)
	}

	npub, err := id.Npub()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(npub, "npub1") {
		t.Errorf("npub oneki yanlis: %q", npub)
	}

	nsec, err := id.Nsec()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(nsec, "nsec1") {
		t.Errorf("nsec oneki yanlis: %q", nsec)
	}
}

func TestGenerateIsRandom(t *testing.T) {
	a, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if a.SecretKey == b.SecretKey {
		t.Error("iki uretim ayni anahtari verdi")
	}
}

func TestFromSecretKeyAcceptsHexAndNsec(t *testing.T) {
	orig, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	nsec, err := orig.Nsec()
	if err != nil {
		t.Fatal(err)
	}

	fromHex, err := identity.FromSecretKey(orig.SecretKey)
	if err != nil {
		t.Fatalf("hex kabul edilmeliydi: %v", err)
	}
	fromNsec, err := identity.FromSecretKey(nsec)
	if err != nil {
		t.Fatalf("nsec kabul edilmeliydi: %v", err)
	}

	if fromHex.PublicKey != orig.PublicKey || fromNsec.PublicKey != orig.PublicKey {
		t.Error("ayni anahtardan farkli pubkey turedi")
	}

	// Bosluklu ve buyuk harfli hex de kabul edilmeli.
	spaced, err := identity.FromSecretKey("  " + strings.ToUpper(orig.SecretKey) + "\n")
	if err != nil {
		t.Fatalf("bosluklu/buyuk harfli hex kabul edilmeliydi: %v", err)
	}
	if spaced.PublicKey != orig.PublicKey {
		t.Error("normalize edilmis hex farkli pubkey verdi")
	}
}

func TestFromSecretKeyRejects(t *testing.T) {
	cases := map[string]string{
		"bos":           "",
		"sadece bosluk": "   ",
		"kisa hex":      "abcd",
		"hex degil":     strings.Repeat("z", 64),
		"npub verildi":  "npub1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"bozuk nsec":    "nsec1bozuk",
	}
	for name, in := range cases {
		if got, err := identity.FromSecretKey(in); err == nil {
			t.Errorf("%s: reddedilmeliydi, donen pubkey %q", name, got.PublicKey)
		}
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alt", "key")

	orig, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(path, orig); err != nil {
		t.Fatalf("Save hata: %v", err)
	}

	got, err := identity.Load(path)
	if err != nil {
		t.Fatalf("Load hata: %v", err)
	}
	if got.SecretKey != orig.SecretKey || got.PublicKey != orig.PublicKey {
		t.Error("roundtrip bozuldu")
	}

	// Dosya nsec olarak ve aciklama satirlariyla yazilmali.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "nsec1") {
		t.Errorf("dosya nsec icermeli:\n%s", text)
	}
	if !strings.Contains(text, "PAYLASMA") {
		t.Errorf("dosya uyari satiri icermeli:\n%s", text)
	}
	if strings.Contains(text, orig.SecretKey) {
		t.Error("ham hex gizli anahtar dosyaya yazilmamali (nsec yeterli)")
	}
}

// Var olan anahtarin uzerine YAZILMAMALI: peer-id degisirse kullanicinin
// payllastigi URL sessizce gecersiz olur.
func TestSaveRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	first, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(path, first); err != nil {
		t.Fatal(err)
	}

	second, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(path, second); err == nil {
		t.Fatal("var olan dosyanin uzerine yazilmamaliydi")
	}

	got, err := identity.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SecretKey != first.SecretKey {
		t.Error("ilk anahtar korunmaliydi")
	}
}

func TestSaveUsesRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows'ta POSIX izinleri yok; koruma ACL'lere kaliyor")
	}
	path := filepath.Join(t.TempDir(), "gizli", "key")

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(path, id); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("dosya izni %o, beklenen 600", perm)
	}
	dst, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dst.Mode().Perm(); perm != 0o700 {
		t.Errorf("dizin izni %o, beklenen 700", perm)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := identity.Load(filepath.Join(t.TempDir(), "yok"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.ErrNotExist beklenirdi, geldi: %v", err)
	}
}

func TestLoadSkipsCommentsAndBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	orig, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	nsec, err := orig.Nsec()
	if err != nil {
		t.Fatal(err)
	}
	content := "# yorum\n\n   \n" + nsec + "\n# son\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := identity.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicKey != orig.PublicKey {
		t.Error("yorumlu dosya dogru okunmadi")
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	for name, content := range map[string]string{
		"bos":          "",
		"sadece yorum": "# yalnizca yorum\n",
		"cop":          "bu bir anahtar degil\n",
		"yarim nsec":   "nsec1abc\n",
	} {
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := identity.Load(path); err == nil {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}
}

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt", "key")

	first, created, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("ilk cagri anahtari uretmeliydi")
	}

	second, created, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("ikinci cagri var olani okumaliydi")
	}
	if second.SecretKey != first.SecretKey {
		t.Error("peer-id degismemeli: ayni anahtar donmeliydi")
	}
}

// Bozuk dosya varsa SESSIZCE yeni anahtar uretilmemeli: peer-id degisirse
// kullanicinin paylastigi URL calismaz ve nedeni gorunmez.
func TestLoadOrCreateDoesNotMaskCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("bozuk icerik\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, created, err := identity.LoadOrCreate(path); err == nil {
		t.Errorf("bozuk dosya hata vermeliydi (created=%v)", created)
	}
}

func TestShortenNpub(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	npub, err := id.Npub()
	if err != nil {
		t.Fatal(err)
	}

	short := identity.ShortenNpub(npub)
	if len(short) >= len(npub) {
		t.Errorf("kisaltilmali: %q", short)
	}
	if !strings.HasPrefix(short, npub[:12]) {
		t.Errorf("ilk 12 karakter korunmali: %q", short)
	}
	if !strings.HasSuffix(short, npub[len(npub)-4:]) {
		t.Errorf("son 4 karakter korunmali: %q", short)
	}
	if id.Short() != short {
		t.Error("Short() ShortenNpub ile ayni olmali")
	}

	if got := identity.ShortenNpub("kisa"); got != "kisa" {
		t.Errorf("kisa deger degismemeli: %q", got)
	}
}

func TestVisitorURL(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	npub, err := id.Npub()
	if err != nil {
		t.Fatal(err)
	}

	got, err := id.VisitorURL("https://kullanici.github.io/PeerPages/", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://kullanici.github.io/PeerPages/#" + npub
	if got != want {
		t.Errorf("URL:\n  geldi:    %s\n  beklenen: %s", got, want)
	}

	// Sondaki bolu eksikse eklenmeli.
	got, err = id.VisitorURL("https://kullanici.github.io/PeerPages", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sondaki bolu eklenmeli: %s", got)
	}

	// Sir varsa `#npub:secret`.
	got, err = id.VisitorURL("https://x.example/", "gizli")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://x.example/#"+npub+":gizli" {
		t.Errorf("sirli URL: %s", got)
	}

	if _, err := id.VisitorURL("", ""); err == nil {
		t.Error("bos app URL'si reddedilmeliydi")
	}
}

func TestDefaultKeyPath(t *testing.T) {
	path, err := identity.DefaultKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "key" {
		t.Errorf("dosya adi 'key' olmali: %s", path)
	}
	if filepath.Base(filepath.Dir(path)) != ".peerpages" {
		t.Errorf("dizin '.peerpages' olmali: %s", path)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("mutlak yol olmali: %s", path)
	}
}
