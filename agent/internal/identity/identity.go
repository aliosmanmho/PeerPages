// Package identity ajanin Nostr kimligini (keypair) yonetir.
//
// Anahtar `~/.peerpages/key` dosyasinda **nsec** (bech32) olarak tutulur.
// Nedenleri: self-describing (nsec oneki ne oldugunu soyler), checksum'u
// var (yanlis kopyalama yakalanir) ve kullanici isterse baska bir Nostr
// aracina aktarabilir. Okuma tarafi ham hex'i de kabul eder.
//
// peer-id = TAM npub. README bolum 4 "npub'in kisa formu" diyordu ama NIP-44
// sifreleme ve `#p` tag'i tam 32 baytlik pubkey gerektiriyor; kisaltilmis
// id'den turetilemez. Karar ve gerekcesi README bolum 8'de.
package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"

	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// Dosya ve dizin izinleri.
//
// Windows'ta POSIX izinleri yok sayilir; dosya kullanici profilinde durur ve
// korumasi isletim sisteminin ACL'lerine kalir (docs/DEV.md'de yazili).
const (
	keyFileMode = 0o600
	keyDirMode  = 0o700
)

// Identity bir Nostr keypair'i.
type Identity struct {
	// SecretKey 32 bayt, hex.
	SecretKey string
	// PublicKey 32 bayt, hex.
	PublicKey string
}

// Generate yeni bir keypair uretir.
func Generate() (Identity, error) {
	sk := gonostr.GeneratePrivateKey()
	return FromSecretKey(sk)
}

// FromSecretKey verilen gizli anahtardan kimlik kurar.
//
// Girdi hem hex hem nsec olabilir.
func FromSecretKey(raw string) (Identity, error) {
	sk, err := normalizeSecretKey(raw)
	if err != nil {
		return Identity{}, err
	}
	pub, err := gonostr.GetPublicKey(sk)
	if err != nil {
		return Identity{}, fmt.Errorf("pubkey turetilemedi: %w", err)
	}
	return Identity{SecretKey: sk, PublicKey: pub}, nil
}

// normalizeSecretKey nsec ya da hex girdiyi hex'e cevirir.
func normalizeSecretKey(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("gizli anahtar bos")
	}

	if strings.HasPrefix(s, "nsec") {
		prefix, value, err := nip19.Decode(s)
		if err != nil {
			return "", fmt.Errorf("nsec okunamadi: %w", err)
		}
		if prefix != "nsec" {
			return "", fmt.Errorf("nsec bekleniyordu, gelen: %q", prefix)
		}
		hex, ok := value.(string)
		if !ok || !gonostr.IsValid32ByteHex(hex) {
			return "", errors.New("nsec icerigi gecersiz")
		}
		return hex, nil
	}

	s = strings.ToLower(s)
	if !gonostr.IsValid32ByteHex(s) {
		return "", errors.New("gizli anahtar 64 karakter hex ya da nsec olmali")
	}
	return s, nil
}

// Npub pubkey'in bech32 (npub) bicimi. peer-id olarak kullanilan deger.
func (i Identity) Npub() (string, error) {
	if i.PublicKey == "" {
		return "", errors.New("pubkey bos")
	}
	return nip19.EncodePublicKey(i.PublicKey)
}

// Nsec gizli anahtarin bech32 bicimi. Yalnizca dosyaya yazmak ve kullaniciya
// bilincli olarak gostermek icin; loglara ASLA girmemeli.
func (i Identity) Nsec() (string, error) {
	if i.SecretKey == "" {
		return "", errors.New("gizli anahtar bos")
	}
	return nip19.EncodePrivateKey(i.SecretKey)
}

// PeerID ziyaretci URL'sinde `#` sonrasina konan deger (tam npub).
func (i Identity) PeerID() (string, error) { return i.Npub() }

// Short ekranda/logda gosterim icin kisaltilmis npub.
//
// README bolum 4'un "okunur form" niyeti burada karsilaniyor: URL tam npub
// tasir, insan gozune kisaltilmis hali gosterilir.
func (i Identity) Short() string {
	npub, err := i.Npub()
	if err != nil {
		return "?"
	}
	return ShortenNpub(npub)
}

// ShortenNpub npub'i okunur bicimde kisaltir: ilk 12 + son 4.
func ShortenNpub(npub string) string {
	if len(npub) <= 20 {
		return npub
	}
	return npub[:12] + "…" + npub[len(npub)-4:]
}

// DefaultKeyPath varsayilan anahtar dosyasi yolu: ~/.peerpages/key
func DefaultKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ev dizini bulunamadi: %w", err)
	}
	return filepath.Join(home, ".peerpages", "key"), nil
}

// Load anahtari dosyadan okur.
func Load(path string) (Identity, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Identity{}, err
	}

	// Dosyada yorum satirlari ve bos satirlar olabilir; ilk anlamli satiri al.
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := FromSecretKey(line)
		if err != nil {
			return Identity{}, fmt.Errorf("%s: %w", path, err)
		}
		return id, nil
	}
	return Identity{}, fmt.Errorf("%s: anahtar bulunamadi (dosya bos)", path)
}

// Save anahtari dosyaya yazar. Var olan dosyanin uzerine YAZMAZ.
func Save(path string, id Identity) error {
	nsec, err := id.Nsec()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, keyDirMode); err != nil {
		return fmt.Errorf("dizin olusturulamadi %s: %w", dir, err)
	}

	// O_EXCL: yaris durumunda bile var olan anahtari ezmeyiz.
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyFileMode)
	if err != nil {
		return fmt.Errorf("anahtar dosyasi olusturulamadi %s: %w", path, err)
	}

	content := "# PeerPages ajan anahtari — BU DOSYAYI KIMSEYLE PAYLASMA.\n" +
		"# Bu anahtari kaybedersen peer-id'n (URL'n) degisir.\n" +
		nsec + "\n"

	if _, werr := f.WriteString(content); werr != nil {
		_ = f.Close()
		return fmt.Errorf("anahtar yazilamadi: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("anahtar dosyasi kapatilamadi: %w", cerr)
	}

	// MkdirAll var olan dizinin iznini degistirmez; acikca daraltalim.
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, keyDirMode)
		_ = os.Chmod(path, keyFileMode)
	}
	return nil
}

// LoadOrCreate anahtari okur; yoksa uretip kaydeder.
//
// created: anahtar bu cagriyla uretildiyse true.
func LoadOrCreate(path string) (id Identity, created bool, err error) {
	id, err = Load(path)
	switch {
	case err == nil:
		return id, false, nil
	case !errors.Is(err, os.ErrNotExist):
		// Dosya var ama okunamadi/bozuk: SESSIZCE yeni anahtar URETMEYIZ.
		// Yeni anahtar peer-id'yi degistirir, yani kullanicinin URL'si
		// sessizce gecersiz olur. Hatayi gostermek daha dogru.
		return Identity{}, false, err
	}

	id, err = Generate()
	if err != nil {
		return Identity{}, false, err
	}
	if err := Save(path, id); err != nil {
		return Identity{}, false, err
	}
	return id, true, nil
}

// VisitorURL ziyaretciye verilecek tam URL'yi uretir.
//
// appURL app shell'in yayinlandigi adres (ornegin
// https://kullanici.github.io/PeerPages/). secret bossa URL'ye eklenmez.
//
// Fragment dilbilgisi (protocol/signaling.md ve web/src/nostr/peerid.ts):
//
//	npub [ "/" hedef ] [ ":" sir ]
//
// Hedef "" ya da "default" ise eklenmez: varsayilan hedef icin kisa URL.
// Sira onemli — hedef sirdan ONCE gelir, cunku sir serbest metindir ve
// icinde "/" bulunabilir.
func (i Identity) VisitorURL(appURL, secret, targetName string) (string, error) {
	npub, err := i.Npub()
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(appURL)
	if base == "" {
		return "", errors.New("app URL'si bos")
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	frag := npub
	if targetName != "" && targetName != target.DefaultName {
		frag += "/" + targetName
	}
	if secret != "" {
		frag += ":" + secret
	}
	return base + "#" + frag, nil
}
