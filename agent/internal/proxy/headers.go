package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// hopByHopHeaders are the headers that are per-connection under RFC
// 9110; they must not be carried end to end. The rule in
// protocol/README.md strips them.
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

// dropFromRequest are the headers that come from the visitor and must
// NOT reach the target.
//
//   - host: rewritten from the target URL (so the visitor's origin does
//     not leak to the local server).
//   - content-length: because the body is rebuilt in chunks, net/http
//     computes it itself.
//   - accept-encoding: the agent does not decompress, so we ask for
//     identity; otherwise we could forward a gzip body to the browser
//     broken.
var dropFromRequest = map[string]bool{
	"host":                      true,
	"content-length":            true,
	"accept-encoding":           true,
	"expect":                    true,
	"upgrade-insecure-requests": true,
}

// dropFromResponse are the headers that come from the target and must
// NOT reach the visitor.
//
// content-length is dropped because the body is split into 16 KiB frames
// and the Service Worker streams the response as a ReadableStream; a
// wrong length breaks the response in the browser. The real length rides
// in the `len` field of the ctrl `end` message.
var dropFromResponse = map[string]bool{
	"content-length":    true,
	"content-encoding":  true,
	"transfer-encoding": true,
}

// SanitizeRequestHeaders turns the headers from the visitor into what
// will be sent to the target.
//
// The input is untrusted (BRIEF principle 4): header names and values are
// validated and invalid ones are dropped silently.
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
	// The agent does not decompress: ask the target for a plain body.
	out.Set("accept-encoding", "identity")
	return out
}

// SanitizeResponseHeaders turns the headers from the target into what
// will be sent to the visitor. Multi-value headers are joined with
// commas; because set-cookie cannot be joined per the RFC, the first
// value is taken and the rest dropped.
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

// isHTML says whether the content-type is HTML.
//
// Parameters (`; charset=utf-8`) are ignored. `application/xhtml+xml`
// counts as HTML too: the browser treats it as a document as well.
func isHTML(contentType string) bool {
	mime := contentType
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	mime = strings.ToLower(strings.TrimSpace(mime))
	return mime == "text/html" || mime == "application/xhtml+xml"
}

// RewriteLocation turns absolute URLs pointing at the target origin into
// the relative path the visitor can see.
//
// For example, with the target http://localhost:3000
//
//	http://localhost:3000/login?x=1  ->  /login?x=1
//
// URLs pointing at another origin are left as they are: hiding a redirect
// is not our job, the visitor decides in their own browser.
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

	// Drop the target's path prefix too, if it has one.
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

// locationHeaders are the headers that carry a URL and need rewriting.
var locationHeaders = []string{"location", "content-location"}

// RewriteLocationHeaders turns the target URLs in the response headers
// into relative paths. The map is modified in place.
func RewriteLocationHeaders(headers map[string]string, base *url.URL) {
	for _, name := range locationHeaders {
		if v, ok := headers[name]; ok {
			headers[name] = RewriteLocation(v, base)
		}
	}
}

// validHeaderName applies the RFC 9110 token rules.
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

// validHeaderValue: a header value must carry no CR/LF or NUL (header
// injection).
func validHeaderValue(s string) bool {
	return !strings.ContainsAny(s, "\r\n\x00")
}
