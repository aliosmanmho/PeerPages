package proxy

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestSanitizeRequestHeadersDropsHopByHop(t *testing.T) {
	in := map[string]string{
		"Accept":              "text/html",
		"User-Agent":          "test",
		"Connection":          "keep-alive",
		"Keep-Alive":          "timeout=5",
		"Transfer-Encoding":   "chunked",
		"Upgrade":             "websocket",
		"TE":                  "trailers",
		"Trailer":             "X-Son",
		"Proxy-Authorization": "Basic xxx",
		"Proxy-Connection":    "keep-alive",
	}
	got := SanitizeRequestHeaders(in)

	for _, drop := range []string{
		"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade",
		"Te", "Trailer", "Proxy-Authorization", "Proxy-Connection",
	} {
		if v := got.Get(drop); v != "" {
			t.Errorf("%s should have been stripped, got: %q", drop, v)
		}
	}
	if got.Get("Accept") != "text/html" {
		t.Errorf("accept has to be preserved, got: %q", got.Get("Accept"))
	}
	if got.Get("User-Agent") != "test" {
		t.Errorf("user-agent has to be kept")
	}
}

func TestSanitizeRequestHeadersDropsHostAndLength(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{
		"host":           "example.github.io",
		"content-length": "123",
		"expect":         "100-continue",
	})
	for _, drop := range []string{"Host", "Content-Length", "Expect"} {
		if v := got.Get(drop); v != "" {
			t.Errorf("%s should have been stripped, got: %q", drop, v)
		}
	}
}

// The agent does not decompress: a plain body is asked of the target.
func TestSanitizeRequestHeadersForcesIdentityEncoding(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{"accept-encoding": "gzip, br"})
	if got.Get("Accept-Encoding") != "identity" {
		t.Errorf("accept-encoding has to be identity, got: %q", got.Get("Accept-Encoding"))
	}
}

// The visitor is untrusted: header injection attempts are dropped.
func TestSanitizeRequestHeadersRejectsInjection(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{
		"x-kotu":              "value\r\nX-Injected: yes",
		"x-null":              "value\x00",
		"x-name with a space": "value",
		"x-parantez(li)":      "value",
		"":                    "value",
		"x-iyi":               "value",
	})
	if got.Get("X-Kotu") != "" {
		t.Error("a value containing CRLF should have been dropped")
	}
	if got.Get("X-Null") != "" {
		t.Error("a value containing NUL should have been dropped")
	}
	if got.Get("X-Bosluklu Ad") != "" {
		t.Error("an invalid header name should have been dropped")
	}
	if got.Get("X-Parantez(li)") != "" {
		t.Error("a header name that is not a token should have been dropped")
	}
	if got.Get("X-Iyi") != "value" {
		t.Error("a valid header should have been kept")
	}
}

func TestSanitizeResponseHeaders(t *testing.T) {
	in := http.Header{
		"Content-Type":      []string{"text/html"},
		"Content-Length":    []string{"1234"},
		"Content-Encoding":  []string{"gzip"},
		"Transfer-Encoding": []string{"chunked"},
		"Connection":        []string{"close"},
		"Cache-Control":     []string{"no-store"},
		"Vary":              []string{"Accept", "Accept-Language"},
	}
	got := SanitizeResponseHeaders(in)

	if got["content-type"] != "text/html" {
		t.Errorf("content-type: %q", got["content-type"])
	}
	// Because the body is split into frames and the SW serves it as a
	// stream, content-length would be wrong; the real length travels in
	// ctrl `end`.`len`.
	for _, drop := range []string{"content-length", "content-encoding", "transfer-encoding", "connection"} {
		if v, ok := got[drop]; ok {
			t.Errorf("%s should have been stripped, got: %q", drop, v)
		}
	}
	if got["vary"] != "Accept, Accept-Language" {
		t.Errorf("a multi-value header has to be joined, got: %q", got["vary"])
	}
	if got["cache-control"] != "no-store" {
		t.Errorf("cache-control: %q", got["cache-control"])
	}
}

func TestSanitizeResponseHeadersSetCookie(t *testing.T) {
	// set-cookie cannot be joined with commas: the first value is
	// taken.
	got := SanitizeResponseHeaders(http.Header{
		"Set-Cookie": []string{"a=1; Path=/", "b=2; Path=/"},
	})
	if got["set-cookie"] != "a=1; Path=/" {
		t.Errorf("set-cookie: %q", got["set-cookie"])
	}
}

func TestRewriteLocation(t *testing.T) {
	base, err := url.Parse("http://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in   string
		want string
	}{
		{"http://localhost:3000/giris", "/giris"},
		{"http://localhost:3000/giris?x=1", "/giris?x=1"},
		{"http://localhost:3000/", "/"},
		{"http://localhost:3000", "/"},
		{"http://LOCALHOST:3000/a", "/a"},
		{"HTTP://localhost:3000/a", "/a"},
		{"http://localhost:3000/a#b", "/a#b"},
		// Relative ones stay as they are.
		{"/zaten/goreli", "/zaten/goreli"},
		{"goreli", "goreli"},
		{"", ""},
		// Another origin: we do not touch it, the decision is in the
		// visitor's browser.
		{"https://example.com/x", "https://example.com/x"},
		{"http://localhost:4000/x", "http://localhost:4000/x"},
		{"http://baska:3000/x", "http://baska:3000/x"},
		// A different scheme.
		{"https://localhost:3000/x", "https://localhost:3000/x"},
	}
	for _, c := range cases {
		if got := RewriteLocation(c.in, base); got != c.want {
			t.Errorf("RewriteLocation(%q) = %q, expected %q", c.in, got, c.want)
		}
	}
}

func TestRewriteLocationWithBasePathPrefix(t *testing.T) {
	base, err := url.Parse("http://localhost:3000/prefix")
	if err != nil {
		t.Fatal(err)
	}
	if got := RewriteLocation("http://localhost:3000/prefix/page", base); got != "/page" {
		t.Errorf("the path prefix has to be dropped, got: %q", got)
	}
}

func TestRewriteLocationHeaders(t *testing.T) {
	base, _ := url.Parse("http://localhost:3000")
	headers := map[string]string{
		"location":         "http://localhost:3000/a",
		"content-location": "http://localhost:3000/b",
		"content-type":     "text/html",
	}
	RewriteLocationHeaders(headers, base)

	want := map[string]string{
		"location":         "/a",
		"content-location": "/b",
		"content-type":     "text/html",
	}
	if !reflect.DeepEqual(headers, want) {
		t.Errorf("got %#v", headers)
	}
}

func TestRewriteLocationNilBase(t *testing.T) {
	if got := RewriteLocation("http://localhost:3000/a", nil); got != "http://localhost:3000/a" {
		t.Errorf("with a nil base the value must not change, got: %q", got)
	}
}
