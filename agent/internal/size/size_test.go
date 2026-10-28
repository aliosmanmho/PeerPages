package size

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1", 1},
		{"1B", 1},
		{"512", 512},
		{"1K", 1024},
		{"1KB", 1024},
		{"1KiB", 1024},
		{"50MB", 50 << 20},
		{"50MiB", 50 << 20},
		{"1GB", 1 << 30},
		{" 2 MB ", 2 << 20},
		{"2mb", 2 << 20},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %d, expected %d", c.in, got, c.want)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "MB", "abc", "-1", "-5MB", "0", "0MB", "1.5MB", "1 XB"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should have returned an error, returned %d", in, got)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512B"},
		{1024, "1KB"},
		{50 << 20, "50MB"},
		{1 << 30, "1GB"},
	}
	for _, c := range cases {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%d) = %q, expected %q", c.in, got, c.want)
		}
	}
}
