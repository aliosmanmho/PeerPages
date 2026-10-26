package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// hopByHopHeaders RFC 9110'a gore baglanti basina gecerli basliklar; uctan uca
// tasinmamalari gerekir. protocol/README.md'deki kural bunlari siler.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// dropFromRequest ziyaretciden gelip hedefe GECMEYECEK basliklar.
//
//   - host: hedef URL'den yeniden yazilir (ziyaretci origin'i yerel sunucuya
//     sizmasin).
//   - content-length: govde chunk'lanarak yeniden kuruldugu icin net/http
//     kendisi hesaplar.
//   - accept-encoding: ajan sikistirmayi acmadigi icin kimlik istenir; aksi
//     halde gzip govdeyi tarayiciya bozuk iletebiliriz.
var dropFromRequest = map[string]bool{
	"host":                      true,
	"content-length":            true,
	"accept-encoding":           true,
	"expect":                    true,
	"upgrade-insecure-requests": true,
}

// dropFromResponse hedeften gelip ziyaretciye GECMEYECEK basliklar.
//
// content-length atlanir cunku govde 16 KiB'lik cercevelere bolunur ve
// Service Worker cevabi ReadableStream ile akitir; yanlis bir uzunluk
// tarayicida cevabi bozar. Gercek uzunluk ctrl `end` mesajindaki `len`
// alaninda tasinir.
var dropFromResponse = map[string]bool{
	"content-length":    true,
	"content-encoding":  true,
	"transfer-encoding": true,
}

// SanitizeRequestHeaders ziyaretciden gelen basliklari hedefe gonderilecek
// hale getirir.
//
// Girdi guvenilmez (README ilke 4): baslik adlari ve degerleri dogrulanir,
// gecersizler sessizce atilir.
func SanitizeRequestHeaders(in map[string]string) http.Header {
	out := make(http.Header, len(in))
	for name, value := range in {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "" || !validHeaderName(lower) || !validHeaderValue(value) {
			continue
		}
		if hopByHopHeaders[lower] || dropFromRequest[lower] {
			continue
		}
		out.Set(lower, value)
	}
	// Ajan sikistirmayi cozmuyor: hedeften duz govde isteyelim.
	out.Set("accept-encoding", "identity")
	return out
}

// SanitizeResponseHeaders hedeften gelen basliklari ziyaretciye gonderilecek
// hale getirir. Cok degerli basliklar virgulle birlestirilir; set-cookie
// RFC geregi birlestirilemedigi icin ilk deger alinir ve digerleri atilir.
func SanitizeResponseHeaders(in http.Header) map[string]string {
	out := make(map[string]string, len(in))
	for name, values := range in {
		lower := strings.ToLower(name)
		if hopByHopHeaders[lower] || dropFromResponse[lower] || len(values) == 0 {
			continue
		}
		if lower == "set-cookie" {
			out[lower] = values[0]
			continue
		}
		out[lower] = strings.Join(values, ", ")
	}
	return out
}

// RewriteLocation hedef origin'ine isaret eden mutlak URL'leri ziyaretcinin
// gorebilecegi goreli yola cevirir.
//
// Ornek: hedef http://localhost:3000 ise
//
//	http://localhost:3000/giris?x=1  ->  /giris?x=1
//
// Baska bir origin'e isaret eden URL'ler oldugu gibi birakilir: yonlendirmeyi
// gizlemek bizim isimiz degil, ziyaretci kendi tarayicisinda karar verir.
func RewriteLocation(value string, base *url.URL) string {
	if value == "" || base == nil {
		return value
	}
	u, err := url.Parse(value)
	if err != nil {
		return value
	}
	if u.Scheme == "" && u.Host == "" {
		return value // zaten goreli
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return value
	}

	// Hedefin yol oneki varsa onu da dus.
	path := u.Path
	if base.Path != "" && base.Path != "/" {
		path = strings.TrimPrefix(path, strings.TrimSuffix(base.Path, "/"))
	}
	if path == "" {
		path = "/"
	}

	rel := &url.URL{Path: path, RawQuery: u.RawQuery, Fragment: u.Fragment}
	return rel.String()
}

// locationHeaders icinde URL tasiyan ve yeniden yazilmasi gereken basliklar.
var locationHeaders = []string{"location", "content-location"}

// RewriteLocationHeaders cevap basliklarindaki hedef URL'lerini goreli yola
// cevirir. Harita yerinde degistirilir.
func RewriteLocationHeaders(headers map[string]string, base *url.URL) {
	for _, name := range locationHeaders {
		if v, ok := headers[name]; ok {
			headers[name] = RewriteLocation(v, base)
		}
	}
}

// validHeaderName RFC 9110 token kurallari.
func validHeaderName(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isTokenByte(s[i]) {
			return false
		}
	}
	return len(s) > 0
}

func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// validHeaderValue baslik degerinde CR/LF ve NUL olmamali (baslik enjeksiyonu).
func validHeaderValue(s string) bool {
	return !strings.ContainsAny(s, "\r\n\x00")
}
