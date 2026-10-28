// Package identity manages the agent's Nostr identity (its keypair).
//
// The key is stored in `~/.peerpages/key` as an **nsec** (bech32). Why:
// it is self-describing (the nsec prefix says what it is), it has a
// checksum (a bad copy gets caught) and the user can move it into another
// Nostr tool if they want. The read side accepts raw hex too.
//
// peer-id = the FULL npub. BRIEF section 4 said "a short form of the
// npub", but NIP-44 encryption and the `#p` tag need the complete 32-byte
// pubkey, which cannot be derived from a shortened id. The decision and
// its reasoning are in BRIEF section 8.
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

// File and directory permissions.
//
// On Windows POSIX permissions are ignored; the file lives in the user
// profile and its protection is left to the operating system's ACLs
// (written down in docs/DEV.md).
const (
	keyFileMode = 0o600
	keyDirMode  = 0o700
)

// Identity is a Nostr keypair.
type Identity struct {
	// SecretKey is 32 bytes, hex.
	SecretKey string
	// PublicKey is 32 bytes, hex.
	PublicKey string
}

// Generate creates a new keypair.
func Generate() (Identity, error) {
	sk := gonostr.GeneratePrivateKey()
	return FromSecretKey(sk)
}

// FromSecretKey builds an identity from the given secret key.
//
// The input may be either hex or an nsec.
func FromSecretKey(raw string) (Identity, error) {
	sk, err := normalizeSecretKey(raw)
	if err != nil {
		return Identity{}, err
	}
	pub, err := gonostr.GetPublicKey(sk)
	if err != nil {
		return Identity{}, fmt.Errorf("the pubkey could not be derived: %w", err)
	}
	return Identity{SecretKey: sk, PublicKey: pub}, nil
}

// normalizeSecretKey turns an nsec or hex input into hex.
func normalizeSecretKey(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("the secret key is empty")
	}

	if strings.HasPrefix(s, "nsec") {
		prefix, value, err := nip19.Decode(s)
		if err != nil {
			return "", fmt.Errorf("the nsec could not be read: %w", err)
		}
		if prefix != "nsec" {
			return "", fmt.Errorf("expected an nsec, got: %q", prefix)
		}
		hex, ok := value.(string)
		if !ok || !gonostr.IsValid32ByteHex(hex) {
			return "", errors.New("the nsec content is invalid")
		}
		return hex, nil
	}

	s = strings.ToLower(s)
	if !gonostr.IsValid32ByteHex(s) {
		return "", errors.New("the secret key must be 64 hex characters or an nsec")
	}
	return s, nil
}

// Npub is the pubkey in bech32 (npub) form. The value used as the
// peer-id.
func (i Identity) Npub() (string, error) {
	if i.PublicKey == "" {
		return "", errors.New("the pubkey is empty")
	}
	return nip19.EncodePublicKey(i.PublicKey)
}

// Nsec is the secret key in bech32 form. Only for writing to the file and
// deliberately showing the user; it must NEVER reach the logs.
func (i Identity) Nsec() (string, error) {
	if i.SecretKey == "" {
		return "", errors.New("the secret key is empty")
	}
	return nip19.EncodePrivateKey(i.SecretKey)
}

// PeerID is the value put after `#` in the visitor URL (the full
// npub).
func (i Identity) PeerID() (string, error) { return i.Npub() }

// Short is the shortened npub for showing on screen or in a log.
//
// This is where BRIEF section 4's "readable form" intent is met: the URL
// carries the full npub, and the human eye is shown the shortened one.
func (i Identity) Short() string {
	npub, err := i.Npub()
	if err != nil {
		return "?"
	}
	return ShortenNpub(npub)
}

// ShortenNpub shortens an npub into a readable form: the first 12 plus
// the last 4.
func ShortenNpub(npub string) string {
	if len(npub) <= 20 {
		return npub
	}
	return npub[:12] + "…" + npub[len(npub)-4:]
}

// DefaultKeyPath is the default key file path: ~/.peerpages/key
func DefaultKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("the home directory could not be found: %w", err)
	}
	return filepath.Join(home, ".peerpages", "key"), nil
}

// Load reads the key from the file.
func Load(path string) (Identity, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Identity{}, err
	}

	// The file may hold comment lines and blank lines; take the first
	// meaningful line.
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
	return Identity{}, fmt.Errorf("%s: no key found (the file is empty)", path)
}

// Save writes the key to the file. It does NOT overwrite an existing
// file.
func Save(path string, id Identity) error {
	nsec, err := id.Nsec()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, keyDirMode); err != nil {
		return fmt.Errorf("the directory could not be created %s: %w", dir, err)
	}

	// O_EXCL: even in a race we never clobber an existing key.
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyFileMode)
	if err != nil {
		return fmt.Errorf("the key file could not be created %s: %w", path, err)
	}

	content := "# PeerPages agent key — DO NOT SHARE THIS FILE WITH ANYONE.\n" +
		"# If you lose this key your peer-id (your URL) changes.\n" +
		nsec + "\n"

	if _, werr := f.WriteString(content); werr != nil {
		_ = f.Close()
		return fmt.Errorf("the key could not be written: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("the key file could not be closed: %w", cerr)
	}

	// MkdirAll does not change the permissions of an existing directory;
	// narrow them explicitly.
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, keyDirMode)
		_ = os.Chmod(path, keyFileMode)
	}
	return nil
}

// LoadOrCreate reads the key, or creates and saves one when there is
// none.
//
// created: true when the key was generated by this call.
func LoadOrCreate(path string) (id Identity, created bool, err error) {
	id, err = Load(path)
	switch {
	case err == nil:
		return id, false, nil
	case !errors.Is(err, os.ErrNotExist):
		// The file exists but could not be read or is corrupt: we do NOT
		// generate a new key SILENTLY. A new key changes the peer-id, so
		// the user's URL would quietly stop working. Showing the error is
		// the right thing.
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

// VisitorURL builds the full URL to hand to a visitor.
//
// appURL is the address the app shell is published at (for example
// https://user.github.io/PeerPages/). An empty secret is not added to the
// URL.
//
// The fragment grammar (protocol/signaling.md and
// web/src/nostr/peerid.ts):
//
//	npub [ "/" target ] [ ":" secret ]
//
// A target of "" or "default" is not added: a short URL for the default
// target. The order matters — the target comes BEFORE the secret, because
// the secret is free text and may contain a "/".
func (i Identity) VisitorURL(appURL, secret, targetName string) (string, error) {
	npub, err := i.Npub()
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(appURL)
	if base == "" {
		return "", errors.New("the app URL is empty")
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
