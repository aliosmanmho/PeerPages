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
		t.Fatalf("Parse(%v) error: %v", specs, err)
	}
	return s
}

func TestParseBareAndNamed(t *testing.T) {
	s := mustParse(t, "http://localhost:3000", "admin=http://127.0.0.1:4000")
	if s.Len() != 2 {
		t.Fatalf("2 targets were expected, got %d", s.Len())
	}
	if !s.Has(DefaultName) || !s.Has("admin") {
		t.Fatalf("the target names are wrong: %v", s.Names())
	}
	got := strings.Join(s.Names(), ",")
	if got != "admin,default" {
		t.Errorf("Names() should be alphabetical, got %q", got)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string][]string{
		"no target":                  {},
		"empty":                      {"   "},
		"no scheme":                  {"localhost:3000"},
		"file scheme":                {"file:///etc/passwd"},
		"ftp scheme":                 {"ftp=ftp://localhost"},
		"no host":                    {"http://"},
		"user information":           {"http://user:pass@localhost:3000"},
		"query":                      {"http://localhost:3000/?a=1"},
		"fragment":                   {"http://localhost:3000/#x"},
		"the same name twice":        {"http://localhost:3000", "http://localhost:4000"},
		"the same name twice, named": {"a=http://localhost:3000", "a=http://localhost:4000"},
		"an invalid name":            {"ad with space=http://localhost:3000"},
		"an empty name":              {"=http://localhost:3000"},
		"a name that is too long":    {strings.Repeat("a", 33) + "=http://localhost:3000"},
	}
	for name, specs := range cases {
		if _, err := Parse(specs); err == nil {
			t.Errorf("%s: Parse(%v) should have returned an error", name, specs)
		}
	}
}

func TestResolveUsesAllowlistHostOnly(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")

	u, err := s.Resolve(DefaultName, "/api/items?x=1")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if u.String() != "http://localhost:3000/api/items?x=1" {
		t.Errorf("unexpected URL: %s", u)
	}
	if u.Host != "localhost:3000" {
		t.Errorf("the host has to come from the allowlist, got: %q", u.Host)
	}
}

func TestResolveKeepsBasePathPrefix(t *testing.T) {
	s := mustParse(t, "http://localhost:3000/onek")
	u, err := s.Resolve(DefaultName, "/a/b")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if u.Path != "/onek/a/b" {
		t.Errorf("the path prefix has to be kept, got: %q", u.Path)
	}
}

func TestResolveUnknownTarget(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")
	_, err := s.Resolve("admin", "/")
	if !errors.Is(err, ErrUnknownTarget) {
		t.Errorf("ErrUnknownTarget was expected, got: %v", err)
	}
}

// The most critical test: a visitor must not be able to make a request
// to another host through the path.
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
			t.Errorf("Resolve(%q) should have been refused, produced: %s", p, u)
			continue
		}
		if !errors.Is(err, ErrBadPath) {
			t.Errorf("Resolve(%q): ErrBadPath was expected, got: %v", p, err)
		}
	}
}

// The base's host must not change under any circumstances.
func TestResolveNeverChangesHost(t *testing.T) {
	s := mustParse(t, "http://127.0.0.1:3000")
	for _, p := range []string{"/", "/a", "/a/b/", "/a?b=c", "/.well-known/x"} {
		u, err := s.Resolve(DefaultName, p)
		if err != nil {
			t.Fatalf("Resolve(%q) error: %v", p, err)
		}
		if u.Scheme != "http" || u.Host != "127.0.0.1:3000" {
			t.Errorf("Resolve(%q) changed the host/scheme: %s", p, u)
		}
	}
}

func TestResolveKeepsTrailingSlash(t *testing.T) {
	s := mustParse(t, "http://localhost:3000")
	u, err := s.Resolve(DefaultName, "/directory/")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if u.Path != "/directory/" {
		t.Errorf("a trailing slash has to be kept, got: %q", u.Path)
	}
}
