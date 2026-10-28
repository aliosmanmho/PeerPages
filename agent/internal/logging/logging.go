// Package logging sets up the agent's structured logging.
//
// BRIEF security principle 6: "by default URL paths are masked in the
// logs (--verbose discloses them)." This package keeps the masking in one
// place; the proxy and CLI layers never log a raw path directly.
package logging

import (
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// New builds a logger according to the verbose flag.
// verbose: the debug level is enabled and paths are not masked.
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// MaskPath makes a request path safe to log.
//
// With verbose true the path comes back as it is. Otherwise every segment
// is reduced to its length and the query is hidden entirely:
//
//	/api/items?x=1  ->  /*3/*5?*3
//	/               ->  /
//
// That way the log keeps the shape (how many segments, how long) without
// leaking the content.
func MaskPath(p string, verbose bool) string {
	if verbose {
		return p
	}

	path, query, hasQuery := strings.Cut(p, "?")

	var b strings.Builder
	if path == "" {
		b.WriteString("/")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		b.WriteString("/*")
		b.WriteString(strconv.Itoa(len(seg)))
	}
	if b.Len() == 0 {
		b.WriteString("/")
	}
	if hasQuery {
		b.WriteString("?*")
		b.WriteString(strconv.Itoa(len(query)))
	}
	return b.String()
}

// PathAttr builds a slog attribute that applies the masking rule.
func PathAttr(p string, verbose bool) slog.Attr {
	return slog.String("path", MaskPath(p, verbose))
}
