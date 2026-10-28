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
		t.Errorf("the secret key is not hex: %q", id.SecretKey)
	}
	if !gonostr.IsValid32ByteHex(id.PublicKey) {
		t.Errorf("the pubkey is not hex: %q", id.PublicKey)
	}

	npub, err := id.Npub()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(npub, "npub1") {
		t.Errorf("the npub prefix is wrong: %q", npub)
	}

	nsec, err := id.Nsec()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(nsec, "nsec1") {
		t.Errorf("the nsec prefix is wrong: %q", nsec)
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
		t.Error("two generations produced the same key")
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
		t.Fatalf("hex should have been accepted: %v", err)
	}
	fromNsec, err := identity.FromSecretKey(nsec)
	if err != nil {
		t.Fatalf("an nsec should have been accepted: %v", err)
	}

	if fromHex.PublicKey != orig.PublicKey || fromNsec.PublicKey != orig.PublicKey {
		t.Error("the same key derived a different pubkey")
	}

	// Hex with whitespace and uppercase has to be accepted too.
	spaced, err := identity.FromSecretKey("  " + strings.ToUpper(orig.SecretKey) + "\n")
	if err != nil {
		t.Fatalf("hex with whitespace or uppercase should have been accepted: %v", err)
	}
	if spaced.PublicKey != orig.PublicKey {
		t.Error("the normalized hex produced a different pubkey")
	}
}

func TestFromSecretKeyRejects(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"whitespace only":  "   ",
		"short hex":        "abcd",
		"not hex":          strings.Repeat("z", 64),
		"npub verildi":     "npub1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"a malformed nsec": "nsec1broken",
	}
	for name, in := range cases {
		if got, err := identity.FromSecretKey(in); err == nil {
			t.Errorf("%s: should have been refused, returned pubkey %q", name, got.PublicKey)
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
		t.Fatalf("Save error: %v", err)
	}

	got, err := identity.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got.SecretKey != orig.SecretKey || got.PublicKey != orig.PublicKey {
		t.Error("the round trip broke")
	}

	// The file has to be written as an nsec, with the comment lines.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "nsec1") {
		t.Errorf("the file has to contain an nsec:\n%s", text)
	}
	if !strings.Contains(text, "DO NOT SHARE") {
		t.Errorf("the file has to contain the warning line:\n%s", text)
	}
	if strings.Contains(text, orig.SecretKey) {
		t.Error("a raw hex secret key must not be written to the file (the nsec is enough)")
	}
}

// An existing key MUST NOT be overwritten: if the peer-id changes, the
// URL the user shared quietly stops working.
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
		t.Fatal("an existing file should not have been overwritten")
	}

	got, err := identity.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SecretKey != first.SecretKey {
		t.Error("the first key should have been kept")
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
		t.Errorf("file permissions %o, expected 600", perm)
	}
	dst, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dst.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory permissions %o, expected 700", perm)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := identity.Load(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.ErrNotExist was expected, got: %v", err)
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
		t.Error("a file with comments was not read correctly")
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	for name, content := range map[string]string{
		"empty":          "",
		"a comment only": "# only a comment\n",
		"cop":            "this is not a key\n",
		"yarim nsec":     "nsec1abc\n",
	} {
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := identity.Load(path); err == nil {
			t.Errorf("%s: should have been refused", name)
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
		t.Error("the first call should have generated the key")
	}

	second, created, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("the second call should have read the existing one")
	}
	if second.SecretKey != first.SecretKey {
		t.Error("the peer-id must not change: the same key should have come back")
	}
}

// With a corrupt file no new key may be generated SILENTLY: if the
// peer-id changes, the URL the user shared stops working and the cause
// stays invisible.
func TestLoadOrCreateDoesNotMaskCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("corrupt content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, created, err := identity.LoadOrCreate(path); err == nil {
		t.Errorf("a corrupt file should have returned an error (created=%v)", created)
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
		t.Errorf("it has to be shortened: %q", short)
	}
	if !strings.HasPrefix(short, npub[:12]) {
		t.Errorf("the first 12 characters have to be kept: %q", short)
	}
	if !strings.HasSuffix(short, npub[len(npub)-4:]) {
		t.Errorf("the last 4 characters have to be kept: %q", short)
	}
	if id.Short() != short {
		t.Error("Short() has to match ShortenNpub")
	}

	if got := identity.ShortenNpub("short"); got != "short" {
		t.Errorf("a short value must not change: %q", got)
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

	got, err := id.VisitorURL("https://kullanici.github.io/PeerPages/", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://kullanici.github.io/PeerPages/#" + npub
	if got != want {
		t.Errorf("URL:\n  got:      %s\n  expected: %s", got, want)
	}

	// A missing trailing slash has to be added.
	got, err = id.VisitorURL("https://kullanici.github.io/PeerPages", "", "default")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("a trailing slash has to be added: %s", got)
	}

	// With a secret it is `#npub:secret`.
	got, err = id.VisitorURL("https://x.example/", "gizli", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://x.example/#"+npub+":gizli" {
		t.Errorf("the URL with a secret: %s", got)
	}

	// A target comes AFTER the npub and BEFORE the secret.
	got, err = id.VisitorURL("https://x.example/", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://x.example/#"+npub+"/admin" {
		t.Errorf("the URL with a target: %s", got)
	}

	got, err = id.VisitorURL("https://x.example/", "gizli", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://x.example/#"+npub+"/admin:gizli" {
		t.Errorf("the URL with a target and a secret: %s", got)
	}

	// "default" is the default target: it is not added to the URL, which
	// stays short.
	got, err = id.VisitorURL("https://x.example/", "", "default")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://x.example/#"+npub {
		t.Errorf("the default target must not be added to the URL: %s", got)
	}

	if _, err := id.VisitorURL("", "", ""); err == nil {
		t.Error("an empty app URL should have been refused")
	}
}

func TestDefaultKeyPath(t *testing.T) {
	path, err := identity.DefaultKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "key" {
		t.Errorf("the file name has to be 'key': %s", path)
	}
	if filepath.Base(filepath.Dir(path)) != ".peerpages" {
		t.Errorf("the directory has to be '.peerpages': %s", path)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("it has to be an absolute path: %s", path)
	}
}
