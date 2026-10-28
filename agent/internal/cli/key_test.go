package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runKeyCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd("test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"key"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestKeyCreatesAndShows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt", "key")

	out, _, err := runKeyCmd(t, "--path", path, "--app-url", "https://ornek.github.io/PeerPages/")
	if err != nil {
		t.Fatalf("key hata: %v\n%s", err, out)
	}

	if !strings.Contains(out, "npub1") {
		t.Errorf("npub basilmaliydi:\n%s", out)
	}
	if !strings.Contains(out, "https://ornek.github.io/PeerPages/#npub1") {
		t.Errorf("ziyaretci URL'si basilmaliydi:\n%s", out)
	}
	if !strings.Contains(out, "short form") {
		t.Errorf("kisa gosterim basilmaliydi:\n%s", out)
	}
	// Gizli anahtar istenmediyse EKRANA GELMEMELI.
	if strings.Contains(out, "nsec1") {
		t.Errorf("gizli anahtar istenmeden basilmamali:\n%s", out)
	}

	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("anahtar dosyasi olusmaliydi: %v", serr)
	}
}

// Ayni dizinde iki kez cagirmak ayni peer-id'yi vermeli: aksi halde
// kullanicinin paylastigi URL sessizce gecersiz olur.
func TestKeyIsStableAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	first, _, err := runKeyCmd(t, "--path", path)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := runKeyCmd(t, "--path", path)
	if err != nil {
		t.Fatal(err)
	}

	npub1 := extractNpub(t, first)
	npub2 := extractNpub(t, second)
	if npub1 != npub2 {
		t.Errorf("peer-id degisti:\n  %s\n  %s", npub1, npub2)
	}
	if strings.Contains(second, "was created") {
		t.Errorf("ikinci cagri var olani okumaliydi:\n%s", second)
	}
}

func TestKeyRevealSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	out, _, err := runKeyCmd(t, "--path", path, "--reveal-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nsec1") {
		t.Errorf("--reveal-secret ile gizli anahtar basilmaliydi:\n%s", out)
	}
	if !strings.Contains(out, "share it with nobody") {
		t.Errorf("uyari metni olmaliydi:\n%s", out)
	}
}

// --new var olan anahtari EZMEMELI ve nedenini soylemeli.
func TestKeyNewRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	first, _, err := runKeyCmd(t, "--path", path)
	if err != nil {
		t.Fatal(err)
	}
	original := extractNpub(t, first)

	_, _, err = runKeyCmd(t, "--path", path, "--new")
	if err == nil {
		t.Fatal("--new var olan dosyayi ezmemeliydi")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("hata mesaji dosya yolunu icermeli: %v", err)
	}

	after, _, err := runKeyCmd(t, "--path", path)
	if err != nil {
		t.Fatal(err)
	}
	if extractNpub(t, after) != original {
		t.Error("anahtar korunmaliydi")
	}
}

func TestKeyNewCreatesWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	out, _, err := runKeyCmd(t, "--path", path, "--new")
	if err != nil {
		t.Fatalf("hata: %v\n%s", err, out)
	}
	if !strings.Contains(out, "a new key was created") {
		t.Errorf("olusturma mesaji beklenirdi:\n%s", out)
	}
}

func TestKeyRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("bozuk\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runKeyCmd(t, "--path", path); err == nil {
		t.Error("bozuk anahtar dosyasi hata vermeliydi")
	}
}

func extractNpub(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, "npub1"); idx >= 0 && strings.Contains(line, "peer-id") {
			return strings.TrimSpace(line[idx:])
		}
	}
	t.Fatalf("ciktida peer-id bulunamadi:\n%s", out)
	return ""
}

// `peerpages version` surum, platform ve Go surumunu basmali: bir hata
// raporunda "hangi binary" sorusunun cevabi --version'in tek satiriyla
// verilemiyor.
func TestVersionCmd(t *testing.T) {
	root := NewRootCmd("v9.9.9")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version komutu hata verdi: %v", err)
	}

	got := out.String()
	for _, want := range []string{"peerpages v9.9.9", "platform:", "go      :"} {
		if !strings.Contains(got, want) {
			t.Errorf("cikti %q icermiyor:\n%s", want, got)
		}
	}
}
