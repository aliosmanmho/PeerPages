// Package target ajanin proxy yapmasina izin verilen yerel hedefleri tutar.
//
// README guvenlik ilkesi 1: "Ajan asla CLI'da verilmeyen bir hosta istek
// atmaz." Bu paket o kurali uygulayan tek yer. Istek yolundan (path) host
// turetmek kasten imkansiz: Resolve yalnizca allowlist'teki taban URL'nin
// scheme/host/port'unu kullanir, path'ten gelen her sey sadece yol olarak
// birlestirilir.
package target

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// DefaultName isimsiz verilen hedefin adi: `--target http://localhost:3000`.
const DefaultName = "default"

var (
	// ErrUnknownTarget allowlist'te olmayan bir hedef istendi (SSRF denemesi
	// ya da hatali URL).
	ErrUnknownTarget = errors.New("hedef allowlist'te yok")
	// ErrBadPath istek yolu gecersiz ya da tehlikeli.
	ErrBadPath = errors.New("gecersiz istek yolu")

	nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
)

// Target tek bir yerel hedef.
type Target struct {
	Name string
	Base *url.URL // scheme + host (+ opsiyonel yol oneki); query/fragment yok
}

// Set adlandirilmis hedeflerin allowlist'i.
type Set struct {
	byName map[string]*Target
	names  []string
}

// Parse `--target` bayraginin tekrarlanan degerlerini ayristirir.
//
// Kabul edilen bicimler:
//
//	http://localhost:3000            -> "default" adiyla
//	admin=http://localhost:4000      -> "admin" adiyla
//
// En az bir hedef zorunludur.
func Parse(specs []string) (*Set, error) {
	if len(specs) == 0 {
		return nil, errors.New("en az bir --target gerekli (ornek: --target http://localhost:3000)")
	}

	s := &Set{byName: make(map[string]*Target, len(specs))}
	for _, spec := range specs {
		name, raw, err := splitSpec(spec)
		if err != nil {
			return nil, err
		}
		base, err := parseBase(raw)
		if err != nil {
			return nil, fmt.Errorf("--target %q: %w", spec, err)
		}
		if _, dup := s.byName[name]; dup {
			return nil, fmt.Errorf("--target %q: %q adi iki kez verildi", spec, name)
		}
		s.byName[name] = &Target{Name: name, Base: base}
		s.names = append(s.names, name)
	}
	return s, nil
}

func splitSpec(spec string) (name, raw string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", errors.New("--target bos verildi")
	}
	// Scheme ile basliyorsa isimsizdir; boylece URL icindeki '=' karakteri
	// yanlislikla ad ayiraci sayilmaz.
	lower := strings.ToLower(spec)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return DefaultName, spec, nil
	}
	n, r, found := strings.Cut(spec, "=")
	if !found {
		return "", "", fmt.Errorf("--target %q: bicim `url` ya da `ad=url` olmali", spec)
	}
	n = strings.TrimSpace(n)
	if !nameRe.MatchString(n) {
		return "", "", fmt.Errorf("--target %q: gecersiz hedef adi %q (izin verilen: harf/rakam/_/-, en fazla 32)", spec, n)
	}
	return n, strings.TrimSpace(r), nil
}

func parseBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL okunamadi: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("yalnizca http/https destekleniyor, verilen: %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("host bos")
	}
	if u.User != nil {
		return nil, errors.New("URL'de kullanici bilgisi olamaz")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return nil, errors.New("hedef URL'de query olamaz")
	}
	if u.Fragment != "" {
		return nil, errors.New("hedef URL'de fragment olamaz")
	}
	// Yol oneki normalize edilir; ".." ile allowlist'in disina cikilamaz.
	u.Path = normalizePrefix(u.Path)
	return u, nil
}

func normalizePrefix(p string) string {
	if p == "" || p == "/" {
		return ""
	}
	clean := path.Clean("/" + p)
	if clean == "/" {
		return ""
	}
	return strings.TrimSuffix(clean, "/")
}

// Names hedef adlarini alfabetik dondurur (ctrl `hello` mesaji icin).
func (s *Set) Names() []string {
	out := make([]string, len(s.names))
	copy(out, s.names)
	sort.Strings(out)
	return out
}

// Has hedef allowlist'te mi.
func (s *Set) Has(name string) bool {
	_, ok := s.byName[name]
	return ok
}

// Len hedef sayisi.
func (s *Set) Len() int { return len(s.names) }

// Resolve bir hedef adi ve ziyaretciden gelen istek yolundan gercek URL uretir.
//
// Guvenlik: scheme/host/port YALNIZCA allowlist'teki taban URL'den gelir.
// reqPath'ten gelen hicbir sey host'u etkilemez; mutlak URL, protokol-goreli
// yol (`//host`), `..` ve null bayt reddedilir.
func (s *Set) Resolve(name, reqPath string) (*url.URL, error) {
	t, ok := s.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTarget, name)
	}

	cleanPath, rawQuery, err := splitRequestPath(reqPath)
	if err != nil {
		return nil, err
	}

	out := *t.Base // kopya: allowlist'teki taban degismez
	out.Path = t.Base.Path + cleanPath
	out.RawQuery = rawQuery
	out.Fragment = ""
	return &out, nil
}

func splitRequestPath(reqPath string) (cleanPath, rawQuery string, err error) {
	if reqPath == "" {
		return "", "", fmt.Errorf("%w: bos", ErrBadPath)
	}
	if strings.ContainsRune(reqPath, 0) {
		return "", "", fmt.Errorf("%w: null bayt", ErrBadPath)
	}
	if strings.ContainsAny(reqPath, "\r\n") {
		return "", "", fmt.Errorf("%w: satir sonu karakteri", ErrBadPath)
	}
	if !strings.HasPrefix(reqPath, "/") {
		return "", "", fmt.Errorf("%w: %q mutlak yol degil", ErrBadPath, reqPath)
	}
	// `//host/...` protokol-goreli URL olarak yorumlanabilir: reddet.
	if strings.HasPrefix(reqPath, "//") {
		return "", "", fmt.Errorf("%w: protokol-goreli yol", ErrBadPath)
	}
	// Ters bolu Windows yollarinda ve bazi ayristiricilarda ayirac sayilir.
	if strings.Contains(reqPath, `\`) {
		return "", "", fmt.Errorf("%w: ters bolu", ErrBadPath)
	}

	p, q, _ := strings.Cut(reqPath, "?")

	// Yuzde kodlamasini cozup `..` kacisini da yakala.
	decoded, derr := url.PathUnescape(p)
	if derr != nil {
		return "", "", fmt.Errorf("%w: bozuk yuzde kodlamasi", ErrBadPath)
	}
	for _, seg := range strings.Split(decoded, "/") {
		if seg == ".." {
			return "", "", fmt.Errorf("%w: `..` segmenti", ErrBadPath)
		}
	}

	clean := path.Clean(p)
	if clean == "/" {
		clean = "/"
	}
	// path.Clean sondaki bolu'yu atar; orijinalde varsa koru (dizin anlamli).
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean, q, nil
}
