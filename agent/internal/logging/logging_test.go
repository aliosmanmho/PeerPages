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
		{"/gizli-token-degeri", "/*18"},
		{"/a/b/c/d", "/*1/*1/*1/*1"},
		{"/trailing/", "/*8"},
		{"/?q=", "/?*2"},
	}
	for _, c := range cases {
		if got := MaskPath(c.in, false); got != c.want {
			t.Errorf("MaskPath(%q, false) = %q, beklenen %q", c.in, got, c.want)
		}
	}
}

func TestMaskPathVerbose(t *testing.T) {
	in := "/api/items?x=1"
	if got := MaskPath(in, true); got != in {
		t.Errorf("verbose modda path degismemeli: %q", got)
	}
}

// Maskeleme gercekten sizdirmiyor mu: cikti icinde ham segment gorunmemeli.
func TestMaskPathLeaksNothing(t *testing.T) {
	secret := "/kullanici/ahmet/anahtar/s3cr3t?token=abcdef"
	masked := MaskPath(secret, false)
	for _, leak := range []string{"ahmet", "s3cr3t", "token", "abcdef", "kullanici", "anahtar"} {
		if strings.Contains(masked, leak) {
			t.Errorf("maskelenmis path %q icinde %q sizdi", masked, leak)
		}
	}
}

func TestNewLevels(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, false).Debug("gorunmemeli")
	if buf.Len() != 0 {
		t.Errorf("verbose kapaliyken debug log yazilmamali, yazilan: %q", buf.String())
	}

	buf.Reset()
	New(&buf, true).Debug("gorunmeli")
	if !strings.Contains(buf.String(), "gorunmeli") {
		t.Errorf("verbose acikken debug log yazilmali, yazilan: %q", buf.String())
	}
}

func TestPathAttr(t *testing.T) {
	if got := PathAttr("/api/items", false).Value.String(); got != "/*3/*5" {
		t.Errorf("PathAttr maskelemedi: %q", got)
	}
}
