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

// The shared-secret layer (protocol/signaling.md).
//
// The point: someone who knows the peer-id (npub) but not the secret
// cannot send the agent a valid offer. An extra layer BENEATH NIP-44.
//
// What the secret does NOT protect: relays still see "this npub received
// something". The secret only guards the right to connect; it does not
// hide that the agent is online.
const (
	// SecretPrefix is the prefix of a sealed payload.
	SecretPrefix = "pps1."
	// secretSalt is the HKDF salt.
	secretSalt = "peerpages-secret-v1"
	// secretInfo is the HKDF info label.
	secretInfo = "signaling"
	// secretAAD is the AEAD associated data.
	secretAAD = "peerpages-signaling-v1"
	// nonceBytes is the ChaCha20-Poly1305 nonce length.
	nonceBytes = chacha20poly1305.NonceSize
)

// Sealer encrypts and decrypts with the shared secret.
//
// A nil Sealer means "no secret", and Seal/Open work with plain JSON.
type Sealer struct {
	key []byte
}

// NewSealer derives the key from the secret.
//
// An empty secret is an error: "there is a secret but it is empty" must
// not quietly turn into no secret at all.
func NewSealer(secret string) (*Sealer, error) {
	if secret == "" {
		return nil, errors.New("the shared secret is empty")
	}
	key, err := deriveKey(secret)
	if err != nil {
		return nil, err
	}
	return &Sealer{key: key}, nil
}

// deriveKey derives a 32-byte key with HKDF-SHA256.
func deriveKey(secret string) ([]byte, error) {
	r := hkdf.New(sha256.New, []byte(secret), []byte(secretSalt), []byte(secretInfo))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("the key could not be derived: %w", err)
	}
	return key, nil
}

// KeyHex returns the derived key as hex. ONLY for checking the test
// vectors; it is not used in production.
func (s *Sealer) KeyHex() string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%x", s.key)
}

// Seal turns canonical JSON into a payload.
//
// With a nil Sealer the payload is plain JSON (no secret).
func (s *Sealer) Seal(canonical string) (string, error) {
	if s == nil {
		return canonical, nil
	}
	var nonce [nonceBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("the nonce could not be generated: %w", err)
	}
	return s.sealWithNonce(nonce[:], canonical)
}

// sealWithNonce encrypts with a fixed nonce. Split out for the test
// vectors; production uses Seal (the nonce must be random for every
// message).
func (s *Sealer) sealWithNonce(nonce []byte, canonical string) (string, error) {
	if len(nonce) != nonceBytes {
		return "", fmt.Errorf("the nonce must be %d bytes, got: %d", nonceBytes, len(nonce))
	}
	aead, err := chacha20poly1305.New(s.key)
	if err != nil {
		return "", fmt.Errorf("the AEAD could not be set up: %w", err)
	}
	ct := aead.Seal(nil, nonce, []byte(canonical), []byte(secretAAD))

	blob := make([]byte, 0, len(nonce)+len(ct))
	blob = append(blob, nonce...)
	blob = append(blob, ct...)
	return SecretPrefix + base64.RawURLEncoding.EncodeToString(blob), nil
}

// IsSealed says whether the payload is sealed.
func IsSealed(payload string) bool {
	return strings.HasPrefix(payload, SecretPrefix)
}

// Open turns a payload back into canonical JSON.
//
// The rules (protocol/signaling.md):
//   - with a Sealer the payload MUST be sealed; plain JSON is rejected
//     (unsealed offers are not accepted),
//   - without a Sealer a sealed payload is rejected (we cannot open it).
func (s *Sealer) Open(payload string) (string, error) {
	sealed := IsSealed(payload)

	if s == nil {
		if sealed {
			return "", errf(CodeBadSecret, "the payload is sealed but no shared secret was given")
		}
		return payload, nil
	}
	if !sealed {
		return "", errf(CodeBadSecret, "a shared secret is required but the payload is not sealed")
	}

	body := strings.TrimPrefix(payload, SecretPrefix)
	blob, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", errf(CodeBadSecret, "base64 could not be decoded")
	}
	if len(blob) < nonceBytes+chacha20poly1305.Overhead {
		return "", errf(CodeBadSecret, "the payload is too short (%d bytes)", len(blob))
	}

	aead, aerr := chacha20poly1305.New(s.key)
	if aerr != nil {
		return "", errf(CodeBadSecret, "the AEAD could not be set up: %v", aerr)
	}
	plain, oerr := aead.Open(nil, blob[:nonceBytes], blob[nonceBytes:], []byte(secretAAD))
	if oerr != nil {
		// Wrong secret, broken tag or tampered data: we do not
		// distinguish.
		return "", errf(CodeBadSecret, "the payload could not be verified (wrong secret or corrupt data)")
	}
	return string(plain), nil
}

// Encode turns a message into a payload (canonical JSON plus the secret
// layer when there is one).
func (s *Sealer) Encode(m Msg) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	return s.Seal(m.Canonical())
}

// Decode turns a payload into a message (opening the secret layer when
// there is one).
func (s *Sealer) Decode(payload string) (Msg, error) {
	canonical, err := s.Open(payload)
	if err != nil {
		return Msg{}, err
	}
	return Parse(canonical)
}
