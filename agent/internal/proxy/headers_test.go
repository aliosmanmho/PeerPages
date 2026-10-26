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
			t.Errorf("%s silinmeliydi, geldi: %q", drop, v)
		}
	}
	if got.Get("Accept") != "text/html" {
		t.Errorf("accept korunmali, geldi: %q", got.Get("Accept"))
	}
	if got.Get("User-Agent") != "test" {
		t.Errorf("user-agent korunmali")
	}
}

func TestSanitizeRequestHeadersDropsHostAndLength(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{
		"host":           "kullanici.github.io",
		"content-length": "123",
		"expect":         "100-continue",
	})
	for _, drop := range []string{"Host", "Content-Length", "Expect"} {
		if v := got.Get(drop); v != "" {
			t.Errorf("%s silinmeliydi, geldi: %q", drop, v)
		}
	}
}

// Ajan sikistirmayi cozmuyor: hedeften duz govde istenir.
func TestSanitizeRequestHeadersForcesIdentityEncoding(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{"accept-encoding": "gzip, br"})
	if got.Get("Accept-Encoding") != "identity" {
		t.Errorf("accept-encoding identity olmali, geldi: %q", got.Get("Accept-Encoding"))
	}
}

// Ziyaretci guvenilmez: baslik enjeksiyonu denemeleri atilir.
func TestSanitizeRequestHeadersRejectsInjection(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{
		"x-kotu":         "deger\r\nX-Enjekte: evet",
		"x-null":         "deger\x00",
		"x-bosluklu ad":  "deger",
		"x-parantez(li)": "deger",
		"":               "deger",
		"x-iyi":          "deger",
	})
	if got.Get("X-Kotu") != "" {
		t.Error("CRLF iceren deger atilmaliydi")
	}
	if got.Get("X-Null") != "" {
		t.Error("NUL iceren deger atilmaliydi")
	}
	if got.Get("X-Bosluklu Ad") != "" {
		t.Error("gecersiz baslik adi atilmaliydi")
	}
	if got.Get("X-Parantez(li)") != "" {
		t.Error("token olmayan baslik adi atilmaliydi")
	}
	if got.Get("X-Iyi") != "deger" {
		t.Error("gecerli baslik korunmaliydi")
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
	// Govde cercevelere bolundugu ve SW akitarak servis ettigi icin
	// content-length yanlis olur; gercek uzunluk ctrl `end`.`len` ile gider.
	for _, drop := range []string{"content-length", "content-encoding", "transfer-encoding", "connection"} {
		if v, ok := got[drop]; ok {
			t.Errorf("%s silinmeliydi, geldi: %q", drop, v)
		}
	}
	if got["vary"] != "Accept, Accept-Language" {
		t.Errorf("cok degerli baslik birlestirilmeli, geldi: %q", got["vary"])
	}
	if got["cache-control"] != "no-store" {
		t.Errorf("cache-control: %q", got["cache-control"])
	}
}

func TestSanitizeResponseHeadersSetCookie(t *testing.T) {
	// set-cookie virgulle birlestirilemez: ilk deger alinir.
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
		// Goreli olanlar oldugu gibi kalir.
		{"/zaten/goreli", "/zaten/goreli"},
		{"goreli", "goreli"},
		{"", ""},
		// Baska origin: dokunmuyoruz, karar ziyaretcinin tarayicisinda.
		{"https://ornek.com/x", "https://ornek.com/x"},
		{"http://localhost:4000/x", "http://localhost:4000/x"},
		{"http://baska:3000/x", "http://baska:3000/x"},
		// Scheme farkli.
		{"https://localhost:3000/x", "https://localhost:3000/x"},
	}
	for _, c := range cases {
		if got := RewriteLocation(c.in, base); got != c.want {
			t.Errorf("RewriteLocation(%q) = %q, beklenen %q", c.in, got, c.want)
		}
	}
}

func TestRewriteLocationWithBasePathPrefix(t *testing.T) {
	base, err := url.Parse("http://localhost:3000/onek")
	if err != nil {
		t.Fatal(err)
	}
	if got := RewriteLocation("http://localhost:3000/onek/sayfa", base); got != "/sayfa" {
		t.Errorf("yol oneki dusurulmeli, geldi: %q", got)
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
		t.Errorf("geldi %#v", headers)
	}
}

func TestRewriteLocationNilBase(t *testing.T) {
	if got := RewriteLocation("http://localhost:3000/a", nil); got != "http://localhost:3000/a" {
		t.Errorf("nil base ile deger degismemeli, geldi: %q", got)
	}
}
