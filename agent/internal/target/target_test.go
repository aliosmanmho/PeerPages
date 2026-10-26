package target

import (
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, specs ...string) *Set {
	t.Helper()
	s, err := Parse(specs)
	if err != nil {
		t.Fatalf("Parse(%v) hata: %v", specs, err)
	}
	return s
}

func TestParseBareAndNamed(t *testing.T) {
	s := mustParse(t, "http://localhost:3000", "admin=http://127.0.0.1:4000")
	if s.Len() != 2 {
		t.Fatalf("2 hedef beklenirdi, %d", s.Len())
	}
	if !s.Has(DefaultName) || !s.Has("admin") {
		t.Fatalf("hedef adlari yanlis: %v", s.Names())
	}
	got := strings.Join(s.Names(), ",")
	if got != "admin,default" {
		t.Errorf("Names() alfabetik olmali, %q", got)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string][]string{
		"hedef yok":            {},
		"bos":                  {"   "},
		"scheme yok":           {"localhost:3000"},
		"file scheme":          {"file:///etc/passwd"},
		"ftp scheme":           {"ftp=ftp://localhost"},
		"host yok":             {"http://"},
		"kullanici bilgisi":    {"http://user:pass@localhost:3000"},
		"query":                {"http://localhost:3000/?a=1"},
		"fragment":             {"http://localhost:3000/#x"},
		"ayni ad iki kez":      {"http://localhost:3000", "http://localhost:4000"},
		"ayni ad iki kez adli": {"a=http://localhost:3000", "a=http://localhost:4000"},
		"gecersiz ad":          {"ad with space=http://localhost:3000"},
		"bos ad":               {"=http://localhost:3000"},
		"cok uzun ad":          {strings.Repeat("a", 33) + "=http://localhost:3000"},
	}
	for name, specs := range cases {
		if _, err := Parse(specs); err == nil {
			t.Errorf("%s: Parse(%v) hata vermeliydi", name, specs)
		}
	}
}

func TestResolveUsesAllowlistHostOnly(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")

	u, err := s.Resolve(DefaultName, "/api/items?x=1")
	if err != nil {
		t.Fatalf("Resolve hata: %v", err)
	}
	if u.String() != "http://localhost:3000/api/items?x=1" {
		t.Errorf("beklenmeyen URL: %s", u)
	}
	if u.Host != "localhost:3000" {
		t.Errorf("host allowlist'ten gelmeli, geldi: %q", u.Host)
	}
}

func TestResolveKeepsBasePathPrefix(t *testing.T) {
	s := mustParse(t, "http://localhost:3000/onek")
	u, err := s.Resolve(DefaultName, "/a/b")
	if err != nil {
		t.Fatalf("Resolve hata: %v", err)
	}
	if u.Path != "/onek/a/b" {
		t.Errorf("yol oneki korunmali, geldi: %q", u.Path)
	}
}

func TestResolveUnknownTarget(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")
	_, err := s.Resolve("admin", "/")
	if !errors.Is(err, ErrUnknownTarget) {
		t.Errorf("ErrUnknownTarget beklenirdi, geldi: %v", err)
	}
}

// En kritik test: ziyaretci path uzerinden baska bir hosta istek attirabilmemeli.
func TestResolveRejectsSSRFAttempts(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")
	attempts := []string{
		"http://evil.example/",
		"//evil.example/x",
		"/../../etc/passwd",
		"/a/../../b",
		"/%2e%2e/%2e%2e/etc/passwd",
		`/a\..\b`,
		"",
		"api/items",
		"/a\x00b",
		"/a\nHost: evil",
		"/a\r\nX: y",
	}
	for _, p := range attempts {
		u, err := s.Resolve(DefaultName, p)
		if err == nil {
			t.Errorf("Resolve(%q) reddedilmeliydi, uretti: %s", p, u)
			continue
		}
		if !errors.Is(err, ErrBadPath) {
			t.Errorf("Resolve(%q): ErrBadPath beklenirdi, geldi: %v", p, err)
		}
	}
}

// Tabanin hostu hicbir kosulda degismemeli.
func TestResolveNeverChangesHost(t *testing.T) {
	s := mustParse(t, "http://127.0.0.1:3000")
	for _, p := range []string{"/", "/a", "/a/b/", "/a?b=c", "/.well-known/x"} {
		u, err := s.Resolve(DefaultName, p)
		if err != nil {
			t.Fatalf("Resolve(%q) hata: %v", p, err)
		}
		if u.Scheme != "http" || u.Host != "127.0.0.1:3000" {
			t.Errorf("Resolve(%q) host/scheme degisti: %s", p, u)
		}
	}
}

func TestResolveKeepsTrailingSlash(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")
	u, err := s.Resolve(DefaultName, "/dizin/")
	if err != nil {
		t.Fatalf("hata: %v", err)
	}
	if u.Path != "/dizin/" {
		t.Errorf("sondaki bolu korunmali, geldi: %q", u.Path)
	}
}
