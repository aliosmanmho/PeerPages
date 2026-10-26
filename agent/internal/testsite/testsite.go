// Package testsite Faz 1 testlerinin hedefi olan kucuk statik siteyi servis
// eder.
//
// Ziyaretci tarayicisinda dogrulanacaklar (README Faz 1 kabul kriterleri):
// HTML, CSS, JS, gorsel, fetch ile JSON ve 5 MB dosya indirme.
//
// Buyuk dosya (big.bin) repoda TUTULMAZ: deterministik olarak uretilir
// (GenerateBig). `make testdata` ya da scripts/*.ps1 bunu cagirir.
package testsite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// BigFileName uretilen buyuk dosyanin adi.
const BigFileName = "big.bin"

// BigFileSize README'deki "5 MB dosya indirme" kabul kriteri.
const BigFileSize = 5 * 1024 * 1024

// bigPattern deterministik icerik: i. bayt (i*31+7) mod 256.
//
// Deterministik olmasi onemli: e2e testi indirdigi dosyanin sha256'sini
// bekledigiyle karsilastiriyor.
func bigPattern(i int64) byte {
	return byte((i*31 + 7) % 256)
}

// GenerateBig buyuk dosyayi uretir. Dosya zaten dogru boyuttaysa dokunmaz.
func GenerateBig(dir string) (path string, created bool, err error) {
	path = filepath.Join(dir, BigFileName)

	if st, statErr := os.Stat(path); statErr == nil && st.Size() == BigFileSize {
		return path, false, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("dizin olusturulamadi: %w", err)
	}

	f, err := os.Create(path) //nolint:gosec // yol cagiran tarafindan verilir (test verisi)
	if err != nil {
		return "", false, fmt.Errorf("dosya olusturulamadi: %w", err)
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
			return "", false, fmt.Errorf("yazilamadi: %w", werr)
		}
		written += n
	}
	return path, true, nil
}

// BigFileSHA256 uretilen buyuk dosyanin beklenen sha256'si (hex).
//
// Dosyayi diske yazmadan hesaplar; e2e testi indirdigi veriyi bununla
// karsilastirir.
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

// Handler statik siteyi servis eden http.Handler uretir.
//
// Ekler:
//   - `/api/items` -> `api/items.json` (uzantisiz API yolu)
//   - `/yonlendir` -> 302, Location mutlak URL (Location yeniden yazimi testi)
//   - `/sse` -> text/event-stream (Faz 6 hazirligi, streaming testi)
func Handler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	mux := http.NewServeMux()

	mux.HandleFunc("/api/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json; charset=utf-8")
		w.Header().Set("cache-control", "no-store")
		http.ServeFile(w, r, filepath.Join(root, "api", "items.json"))
	})

	mux.HandleFunc("/yonlendir", func(w http.ResponseWriter, r *http.Request) {
		// Mutlak URL: ajan bunu goreli yola cevirmeli.
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		w.Header().Set("location", scheme+"://"+r.Host+"/index.html")
		w.WriteHeader(http.StatusFound)
	})

	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-store")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flush desteklenmiyor", http.StatusInternalServerError)
			return
		}
		for i := 0; i < 3; i++ {
			if _, err := io.WriteString(w, fmt.Sprintf("data: olay-%d\n\n", i)); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// big.bin'i tarayici indirme olarak isaretle.
		if strings.HasSuffix(r.URL.Path, "/"+BigFileName) {
			w.Header().Set("content-type", "application/octet-stream")
		}
		fileServer.ServeHTTP(w, r)
	})

	return mux
}
