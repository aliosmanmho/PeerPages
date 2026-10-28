// Package testsite serves the small static site the phase 1 tests point
// at.
//
// What has to be verified in the visitor's browser (the BRIEF phase 1
// acceptance criteria): HTML, CSS, JS, an image, JSON over fetch and a
// 5 MB file download.
//
// The large file (big.bin) is NOT KEPT in the repo: it is generated
// deterministically (GenerateBig). `make testdata` or scripts/*.ps1 calls
// that.
package testsite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// BigFileName is the name of the generated large file.
const BigFileName = "big.bin"

// BigFileSize is the "5 MB file download" acceptance criterion from
// BRIEF.
const BigFileSize = 5 * 1024 * 1024

// bigPattern is the deterministic content: byte i is (i*31+7) mod 256.
//
// Being deterministic matters: the e2e test compares the sha256 of the
// file it downloaded against the expected one.
func bigPattern(i int64) byte {
	return byte((i*31 + 7) % 256)
}

// GenerateBig generates the large file. If the file is already the right
// size it is left alone.
func GenerateBig(dir string) (path string, created bool, err error) {
	path = filepath.Join(dir, BigFileName)

	if st, statErr := os.Stat(path); statErr == nil && st.Size() == BigFileSize {
		return path, false, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("the directory could not be created: %w", err)
	}

	f, err := os.Create(path) //nolint:gosec // the path comes from the caller (test data)
	if err != nil {
		return "", false, fmt.Errorf("the file could not be created: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	const block = 64 * 1024
	buf := make([]byte, block)
	var written int64
	for written < BigFileSize {
		n := int64(block)
		if BigFileSize-written < n {
			n = BigFileSize - written
		}
		for i := int64(0); i < n; i++ {
			buf[i] = bigPattern(written + i)
		}
		if _, werr := f.Write(buf[:n]); werr != nil {
			return "", false, fmt.Errorf("could not write: %w", werr)
		}
		written += n
	}
	return path, true, nil
}

// BigFileSHA256 is the expected sha256 of the generated large file
// (hex).
//
// It is computed without writing the file to disk; the e2e test compares
// what it downloaded against this.
func BigFileSHA256() string {
	h := sha256.New()
	const block = 64 * 1024
	buf := make([]byte, block)
	var written int64
	for written < BigFileSize {
		n := int64(block)
		if BigFileSize-written < n {
			n = BigFileSize - written
		}
		for i := int64(0); i < n; i++ {
			buf[i] = bigPattern(written + i)
		}
		_, _ = h.Write(buf[:n])
		written += n
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Handler builds the http.Handler that serves the static site.
//
// On top of the files:
//   - `/api/items` -> `api/items.json` (an API path with no extension)
//   - `/redirect`  -> 302 with an absolute Location (the Location
//     rewriting test)
//   - `/sse` -> text/event-stream (`?n=`, `?delay=`, `?keepalive=`)
//   - `/ws`  -> a WebSocket echo (`?close=`, `?proto=`)
func Handler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	mux := http.NewServeMux()

	mux.HandleFunc("/api/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json; charset=utf-8")
		w.Header().Set("cache-control", "no-store")
		http.ServeFile(w, r, filepath.Join(root, "api", "items.json"))
	})

	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		// An absolute URL: the agent has to rewrite it to a relative
		// path.
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		w.Header().Set("location", scheme+"://"+r.Host+"/index.html")
		w.WriteHeader(http.StatusFound)
	})

	mux.HandleFunc("/sse", sseHandler)
	mux.HandleFunc("/ws", wsHandler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Mark big.bin as a browser download.
		if strings.HasSuffix(r.URL.Path, "/"+BigFileName) {
			w.Header().Set("content-type", "application/octet-stream")
		}
		fileServer.ServeHTTP(w, r)
	})

	return mux
}
