package nostr

import (
	"fmt"

	"github.com/nbd-wtf/go-nostr/nip44"
)

// NIP-44: the signaling content is always encrypted (BRIEF section 6,
// principle 2). Relays only see "a pubkey received something".
//
// The conversation key comes out the same on both sides through ECDH, so
// encryption and decryption use the same pair of functions: the sender
// (own sk, recipient pub), the receiver (own sk, sender pub).

// Encrypt encrypts the payload to the recipient.
func Encrypt(secretKey, peerPubKey, payload string) (string, error) {
	key, err := nip44.GenerateConversationKey(peerPubKey, secretKey)
	if err != nil {
		return "", fmt.Errorf("the conversation key could not be generated: %w", err)
	}
	out, err := nip44.Encrypt(payload, key)
	if err != nil {
		return "", fmt.Errorf("NIP-44 encryption: %w", err)
	}
	return out, nil
}

// Decrypt decrypts the content that came from the sender.
func Decrypt(secretKey, peerPubKey, content string) (string, error) {
	key, err := nip44.GenerateConversationKey(peerPubKey, secretKey)
	if err != nil {
		return "", fmt.Errorf("the conversation key could not be generated: %w", err)
	}
	out, err := nip44.Decrypt(content, key)
	if err != nil {
		return "", fmt.Errorf("NIP-44 decryption: %w", err)
	}
	return out, nil
}
