// Package target holds the local targets the agent is allowed to proxy
// to.
//
// BRIEF security principle 1: "the agent never makes a request to a host
// that was not given on the CLI." This package is the one place that
// enforces it. Deriving a host from the request path is deliberately
// impossible: Resolve only uses the scheme/host/port of the base URL from
// the allowlist, and everything from the path is joined as a path only.
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

// DefaultName is the name of a target given without one:
// `--target http://localhost:3000`.
const DefaultName = "default"

var (
	// ErrUnknownTarget means a target that is not in the allowlist was
	// asked for (an SSRF attempt or a wrong URL).
	ErrUnknownTarget = errors.New("the target is not in the allowlist")
	// ErrBadPath means the request path is invalid or dangerous.
	ErrBadPath = errors.New("invalid request path")

	nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
)

// Target is a single local target.
type Target struct {
	Name string
	Base *url.URL // scheme + host (+ opsiyonel yol oneki); query/fragment yok
}

// Set is the allowlist of named targets.
type Set struct {
	byName map[string]*Target
	names  []string
}

// Parse parses the repeated values of the `--target` flag.
//
// The accepted forms:
//
//	http://localhost:3000            -> under the name "default"
//	admin=http://localhost:4000      -> under the name "admin"
//
// At least one target is required.
func Parse(specs []string) (*Set, error) {
	if len(specs) == 0 {
		return nil, errors.New("at least one --target is required (for example: --target http://localhost:3000)")
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
			return nil, fmt.Errorf("--target %q: the name %q was given twice", spec, name)
		}
		s.byName[name] = &Target{Name: name, Base: base}
		s.names = append(s.names, name)
	}
	return s, nil
}

func splitSpec(spec string) (name, raw string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", errors.New("--target was given empty")
	}
	// If it starts with a scheme it has no name, so an '=' inside the URL
	// is not mistaken for the name separator.
	lower := strings.ToLower(spec)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return DefaultName, spec, nil
	}
	n, r, found := strings.Cut(spec, "=")
	if !found {
		return "", "", fmt.Errorf("--target %q: the form must be `url` or `name=url`", spec)
	}
	n = strings.TrimSpace(n)
	if !nameRe.MatchString(n) {
		return "", "", fmt.Errorf("--target %q: invalid target name %q (allowed: letters/digits/_/-, at most 32)", spec, n)
	}
	return n, strings.TrimSpace(r), nil
}

func parseBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("the URL could not be read: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("only http/https are supported, got: %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("the host is empty")
	}
	if u.User != nil {
		return nil, errors.New("the URL must not carry user information")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return nil, errors.New("a target URL must not have a query")
	}
	if u.Fragment != "" {
		return nil, errors.New("a target URL must not have a fragment")
	}
	// The path prefix is normalized; ".." cannot escape the allowlist.
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

// Names returns the target names in alphabetical order (for the ctrl
// `hello` message).
func (s *Set) Names() []string {
	out := make([]string, len(s.names))
	copy(out, s.names)
	sort.Strings(out)
	return out
}

// Has says whether the target is in the allowlist.
func (s *Set) Has(name string) bool {
	_, ok := s.byName[name]
	return ok
}

// Len is the number of targets.
func (s *Set) Len() int { return len(s.names) }

// Resolve builds the real URL from a target name and the request path
// that came from the visitor.
//
// Security: the scheme/host/port come ONLY from the base URL in the
// allowlist. Nothing from reqPath can affect the host; an absolute URL, a
// protocol-relative path (`//host`), `..` and a null byte are all
// rejected.
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
		return "", "", fmt.Errorf("%w: empty", ErrBadPath)
	}
	if strings.ContainsRune(reqPath, 0) {
		return "", "", fmt.Errorf("%w: null byte", ErrBadPath)
	}
	if strings.ContainsAny(reqPath, "\r\n") {
		return "", "", fmt.Errorf("%w: line break character", ErrBadPath)
	}
	if !strings.HasPrefix(reqPath, "/") {
		return "", "", fmt.Errorf("%w: %q is not an absolute path", ErrBadPath, reqPath)
	}
	// `//host/...` can be read as a protocol-relative URL: refuse it.
	if strings.HasPrefix(reqPath, "//") {
		return "", "", fmt.Errorf("%w: protocol-relative path", ErrBadPath)
	}
	// A backslash counts as a separator in Windows paths and in some
	// parsers.
	if strings.Contains(reqPath, `\`) {
		return "", "", fmt.Errorf("%w: backslash", ErrBadPath)
	}

	p, q, _ := strings.Cut(reqPath, "?")

	// Decode percent escapes so a `..` escape is caught too.
	decoded, derr := url.PathUnescape(p)
	if derr != nil {
		return "", "", fmt.Errorf("%w: broken percent encoding", ErrBadPath)
	}
	for _, seg := range strings.Split(decoded, "/") {
		if seg == ".." {
			return "", "", fmt.Errorf("%w: `..` segment", ErrBadPath)
		}
	}

	clean := path.Clean(p)
	if clean == "/" {
		clean = "/"
	}
	// path.Clean drops a trailing slash; keep it if the original had one
	// (a directory means something).
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean, q, nil
}
