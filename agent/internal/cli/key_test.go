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

	out, _, err := runKeyCmd(t, "--path", path, "--app-url", "https://example.github.io/PeerPages/")
	if err != nil {
		t.Fatalf("key error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "npub1") {
		t.Errorf("the npub should have been printed:\n%s", out)
	}
	if !strings.Contains(out, "https://example.github.io/PeerPages/#npub1") {
		t.Errorf("the visitor URL should have been printed:\n%s", out)
	}
	if !strings.Contains(out, "short form") {
		t.Errorf("the short form should have been printed:\n%s", out)
	}
	// Unless it was asked for, the secret key MUST NOT REACH THE
	// SCREEN.
	if strings.Contains(out, "nsec1") {
		t.Errorf("the secret key must not be printed unasked:\n%s", out)
	}

	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("the key file should have been created: %v", serr)
	}
}

// Two calls in the same directory have to give the same peer-id:
// otherwise the URL the user shared quietly stops working.
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
		t.Errorf("the peer-id changed:\n  %s\n  %s", npub1, npub2)
	}
	if strings.Contains(second, "was created") {
		t.Errorf("the second call should have read the existing one:\n%s", second)
	}
}

func TestKeyRevealSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	out, _, err := runKeyCmd(t, "--path", path, "--reveal-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nsec1") {
		t.Errorf("--reveal-secret should have printed the secret key:\n%s", out)
	}
	if !strings.Contains(out, "share it with nobody") {
		t.Errorf("there should have been a warning:\n%s", out)
	}
}

// --new MUST NOT clobber an existing key, and has to say why.
func TestKeyNewRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	first, _, err := runKeyCmd(t, "--path", path)
	if err != nil {
		t.Fatal(err)
	}
	original := extractNpub(t, first)

	_, _, err = runKeyCmd(t, "--path", path, "--new")
	if err == nil {
		t.Fatal("--new should not have clobbered the existing file")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error message has to contain the file path: %v", err)
	}

	after, _, err := runKeyCmd(t, "--path", path)
	if err != nil {
		t.Fatal(err)
	}
	if extractNpub(t, after) != original {
		t.Error("the key should have been preserved")
	}
}

func TestKeyNewCreatesWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")

	out, _, err := runKeyCmd(t, "--path", path, "--new")
	if err != nil {
		t.Fatalf("error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "a new key was created") {
		t.Errorf("a creation message was expected:\n%s", out)
	}
}

func TestKeyRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runKeyCmd(t, "--path", path); err == nil {
		t.Error("a corrupt key file should have returned an error")
	}
}

func extractNpub(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, "npub1"); idx >= 0 && strings.Contains(line, "peer-id") {
			return strings.TrimSpace(line[idx:])
		}
	}
	t.Fatalf("no peer-id was found in the output:\n%s", out)
	return ""
}

// `peerpages version` has to print the version, the platform and the Go
// version: in a bug report the question "which binary" cannot be answered
// by the single line --version gives.
func TestVersionCmd(t *testing.T) {
	root := NewRootCmd("v9.9.9")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("the version command returned an error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"peerpages v9.9.9", "platform:", "go      :"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not contain %q:\n%s", want, got)
		}
	}
}
