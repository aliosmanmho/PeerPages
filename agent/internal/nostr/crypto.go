package nostr

import (
	"fmt"

	"github.com/nbd-wtf/go-nostr/nip44"
)

// NIP-44: signaling icerigi her zaman sifreli (BRIEF bolum 6 ilke 2).
// Relay'ler yalnizca "bir pubkey bir sey aldi" gorur.
//
// Konusma anahtari ECDH ile iki taraftan da ayni cikar, bu yuzden sifreleme
// ve cozme ayni fonksiyon cifti ile yapilir: gonderen (kendi sk, alici pub),
// alan (kendi sk, gonderen pub).

// Encrypt payload'i aliciya sifreler.
func Encrypt(secretKey, peerPubKey, payload string) (string, error) {
	key, err := nip44.GenerateConversationKey(peerPubKey, secretKey)
	if err != nil {
		return "", fmt.Errorf("konusma anahtari uretilemedi: %w", err)
	}
	out, err := nip44.Encrypt(payload, key)
	if err != nil {
		return "", fmt.Errorf("NIP-44 sifreleme: %w", err)
	}
	return out, nil
}

// Decrypt gonderenden gelen icerigi cozer.
func Decrypt(secretKey, peerPubKey, content string) (string, error) {
	key, err := nip44.GenerateConversationKey(peerPubKey, secretKey)
	if err != nil {
		return "", fmt.Errorf("konusma anahtari uretilemedi: %w", err)
	}
	out, err := nip44.Decrypt(content, key)
	if err != nil {
		return "", fmt.Errorf("NIP-44 cozme: %w", err)
	}
	return out, nil
}
