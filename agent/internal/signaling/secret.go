package signaling

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// Paylasilan sir katmani (protocol/signaling.md).
//
// Amac: peer-id'yi (npub) bilen ama sirri bilmeyen birinin ajana gecerli bir
// offer gonderememesi. NIP-44'un ALTINDA ek bir katman.
//
// Sir neyi KORUMAZ: relay'ler yine "bu npub bir sey aldi" gorur. Sir yalnizca
// baglanma yetkisini korur, ajanin cevrimici oldugunu gizlemez.
const (
	// SecretPrefix sirli payload oneki.
	SecretPrefix = "pps1."
	// secretSalt HKDF tuzu.
	secretSalt = "peerpages-secret-v1"
	// secretInfo HKDF info etiketi.
	secretInfo = "signaling"
	// secretAAD AEAD iliskili verisi.
	secretAAD = "peerpages-signaling-v1"
	// nonceBytes ChaCha20-Poly1305 nonce uzunlugu.
	nonceBytes = chacha20poly1305.NonceSize
)

// Sealer paylasilan sirla sifreleme/cozme yapar.
//
// Nil Sealer "sir yok" anlamina gelir ve Seal/Open duz JSON ile calisir.
type Sealer struct {
	key []byte
}

// NewSealer sirdan anahtar turetir.
//
// Bos sir hata verir: "sir var ama bos" sessizce sirsiza donmemeli.
func NewSealer(secret string) (*Sealer, error) {
	if secret == "" {
		return nil, errors.New("paylasilan sir bos")
	}
	key, err := deriveKey(secret)
	if err != nil {
		return nil, err
	}
	return &Sealer{key: key}, nil
}

// deriveKey HKDF-SHA256 ile 32 baytlik anahtar turetir.
func deriveKey(secret string) ([]byte, error) {
	r := hkdf.New(sha256.New, []byte(secret), []byte(secretSalt), []byte(secretInfo))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("anahtar turetilemedi: %w", err)
	}
	return key, nil
}

// KeyHex turetilen anahtari hex olarak dondurur. YALNIZCA test vektorlerini
// dogrulamak icin; uretimde kullanilmaz.
func (s *Sealer) KeyHex() string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%x", s.key)
}

// Seal kanonik JSON'u payload'a cevirir.
//
// Sealer nil ise payload duz JSON'dur (sir yok).
func (s *Sealer) Seal(canonical string) (string, error) {
	if s == nil {
		return canonical, nil
	}
	var nonce [nonceBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("nonce uretilemedi: %w", err)
	}
	return s.sealWithNonce(nonce[:], canonical)
}

// sealWithNonce sabit nonce ile sifreler. Test vektorleri icin ayrildi;
// uretimde Seal kullanilir (nonce her mesajda rastgele olmali).
func (s *Sealer) sealWithNonce(nonce []byte, canonical string) (string, error) {
	if len(nonce) != nonceBytes {
		return "", fmt.Errorf("nonce %d bayt olmali, verilen: %d", nonceBytes, len(nonce))
	}
	aead, err := chacha20poly1305.New(s.key)
	if err != nil {
		return "", fmt.Errorf("AEAD kurulamadi: %w", err)
	}
	ct := aead.Seal(nil, nonce, []byte(canonical), []byte(secretAAD))

	blob := make([]byte, 0, len(nonce)+len(ct))
	blob = append(blob, nonce...)
	blob = append(blob, ct...)
	return SecretPrefix + base64.RawURLEncoding.EncodeToString(blob), nil
}

// IsSealed payload sirli mi.
func IsSealed(payload string) bool {
	return strings.HasPrefix(payload, SecretPrefix)
}

// Open payload'i kanonik JSON'a cevirir.
//
// Kurallar (protocol/signaling.md):
//   - Sealer varsa payload sirli OLMAK ZORUNDA; duz JSON reddedilir
//     (sirsiz teklifler kabul edilmez).
//   - Sealer yoksa sirli payload reddedilir (cozemeyiz).
func (s *Sealer) Open(payload string) (string, error) {
	sealed := IsSealed(payload)

	if s == nil {
		if sealed {
			return "", errf(CodeBadSecret, "payload sirli ama paylasilan sir verilmedi")
		}
		return payload, nil
	}
	if !sealed {
		return "", errf(CodeBadSecret, "paylasilan sir gerekli ama payload sirsiz")
	}

	body := strings.TrimPrefix(payload, SecretPrefix)
	blob, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", errf(CodeBadSecret, "base64 cozulemedi")
	}
	if len(blob) < nonceBytes+chacha20poly1305.Overhead {
		return "", errf(CodeBadSecret, "payload cok kisa (%d bayt)", len(blob))
	}

	aead, aerr := chacha20poly1305.New(s.key)
	if aerr != nil {
		return "", errf(CodeBadSecret, "AEAD kurulamadi: %v", aerr)
	}
	plain, oerr := aead.Open(nil, blob[:nonceBytes], blob[nonceBytes:], []byte(secretAAD))
	if oerr != nil {
		// Yanlis sir, bozuk etiket ya da oynanmis veri: ayrim yapmiyoruz.
		return "", errf(CodeBadSecret, "payload dogrulanamadi (yanlis sir ya da bozuk veri)")
	}
	return string(plain), nil
}

// Encode mesaji payload'a cevirir (kanonik JSON + varsa sir katmani).
func (s *Sealer) Encode(m Msg) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	return s.Seal(m.Canonical())
}

// Decode payload'i mesaja cevirir (varsa sir katmanini acar).
func (s *Sealer) Decode(payload string) (Msg, error) {
	canonical, err := s.Open(payload)
	if err != nil {
		return Msg{}, err
	}
	return Parse(canonical)
}
