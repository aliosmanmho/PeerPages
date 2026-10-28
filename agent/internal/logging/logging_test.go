package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestMaskPathMasked(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/", "/"},
		{"", "/"},
		{"/api/items", "/*3/*5"},
		{"/api/items?x=1", "/*3/*5?*3"},
		{"/secret-token-value", "/*18"},
		{"/a/b/c/d", "/*1/*1/*1/*1"},
		{"/trailing/", "/*8"},
		{"/?q=", "/?*2"},
	}
	for _, c := range cases {
		if got := MaskPath(c.in, false); got != c.want {
			t.Errorf("MaskPath(%q, false) = %q, expected %q", c.in, got, c.want)
		}
	}
}

func TestMaskPathVerbose(t *testing.T) {
	in := "/api/items?x=1"
	if got := MaskPath(in, true); got != in {
		t.Errorf("in verbose mode the path must not change: %q", got)
	}
}

// Does the masking really not leak: no raw segment may show up in the
// output.
func TestMaskPathLeaksNothing(t *testing.T) {
	secret := "/users/alex/key/s3cr3t?token=abcdef"
	masked := MaskPath(secret, false)
	for _, leak := range []string{"ahmet", "s3cr3t", "token", "abcdef", "user", "key"} {
		if strings.Contains(masked, leak) {
			t.Errorf("%q leaked inside the masked path %q", masked, leak)
		}
	}
}

func TestNewLevels(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, false).Debug("gorunmemeli")
	if buf.Len() != 0 {
		t.Errorf("with verbose off no debug log should be written, written: %q", buf.String())
	}

	buf.Reset()
	New(&buf, true).Debug("gorunmeli")
	if !strings.Contains(buf.String(), "gorunmeli") {
		t.Errorf("with verbose on a debug log has to be written, written: %q", buf.String())
	}
}

func TestPathAttr(t *testing.T) {
	if got := PathAttr("/api/items", false).Value.String(); got != "/*3/*5" {
		t.Errorf("PathAttr did not mask: %q", got)
	}
}
