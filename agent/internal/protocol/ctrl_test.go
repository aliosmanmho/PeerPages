package protocol

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func ctrlCode(t *testing.T, err error) CtrlErrorCode {
	t.Helper()
	var ce *CtrlError
	if !errors.As(err, &ce) {
		t.Fatalf("a CtrlError was expected, got: %T (%v)", err, err)
	}
	return ce.Code
}

func TestCtrlValidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Ctrl.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := ParseCtrl(vec.Text)
			if err != nil {
				t.Fatalf("ParseCtrl error: %v", err)
			}
			// The canonical output has to be byte-for-byte the same as
			// the TS side's.
			if got := msg.Canonical(); got != vec.Canonical {
				t.Errorf("the canonical output differs:\n  got:      %s\n  expected: %s", got, vec.Canonical)
			}
			// Reparsing the canonical text has to give the same
			// message.
			again, err := ParseCtrl(msg.Canonical())
			if err != nil {
				t.Fatalf("the canonical text could not be reparsed: %v", err)
			}
			if !reflect.DeepEqual(again, msg) {
				t.Errorf("the round trip broke:\n  %#v\n  %#v", again, msg)
			}
		})
	}
}

func TestCtrlValidVectorsCoverAllTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, vec := range loadVectors(t).Ctrl.Valid {
		msg, err := ParseCtrl(vec.Text)
		if err != nil {
			t.Fatalf("%s: %v", vec.Name, err)
		}
		seen[msg.Type()] = true
	}
	got := make([]string, 0, len(seen))
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{
		"abort", "end", "hello", "ping", "pong", "req", "res",
		"ws-close", "ws-ok", "ws-open",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("every type has to be covered:\n  got:      %v\n  expected: %v", got, want)
	}
}

func TestCtrlInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Ctrl.Invalid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := ParseCtrl(vec.Text)
			if err == nil {
				t.Fatalf("an error was expected (%s), returned: %#v", vec.Error, msg)
			}
			if code := ctrlCode(t, err); string(code) != vec.Error {
				t.Errorf("error code %q, expected %q (detail: %v)", code, vec.Error, err)
			}
		})
	}
}

func TestParseCtrlFields(t *testing.T) {
	t.Run("req", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"req","id":17,"method":"GET","path":"/api/items?x=1","headers":{"Accept":"text/html"},"hasBody":false}`)
		if err != nil {
			t.Fatal(err)
		}
		want := Req{
			ID:      17,
			Method:  "GET",
			Path:    "/api/items?x=1",
			Headers: map[string]string{"accept": "text/html"}, // kucuk harfe normalize
			HasBody: false,
		}
		if !reflect.DeepEqual(msg, want) {
			t.Errorf("got %#v", msg)
		}
	})

	t.Run("res", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"res","id":3,"status":404,"headers":{}}`)
		if err != nil {
			t.Fatal(err)
		}
		want := Res{ID: 3, Status: 404, Headers: map[string]string{}}
		if !reflect.DeepEqual(msg, want) {
			t.Errorf("got %#v", msg)
		}
	})

	t.Run("hello", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"hello","v":1,"targets":["default","admin"],"auth":"required"}`)
		if err != nil {
			t.Fatal(err)
		}
		want := Hello{V: 1, Targets: []string{"default", "admin"}, Auth: AuthRequired}
		if !reflect.DeepEqual(msg, want) {
			t.Errorf("got %#v", msg)
		}
	})

	t.Run("ping pong", func(t *testing.T) {
		if msg, err := ParseCtrl(`{"t":"ping"}`); err != nil || msg != (Ping{}) {
			t.Errorf("ping: %#v %v", msg, err)
		}
		if msg, err := ParseCtrl(`{"t":"pong"}`); err != nil || msg != (Pong{}) {
			t.Errorf("pong: %#v %v", msg, err)
		}
	})

	t.Run("unknown extra fields are ignored", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"end","id":1,"frames":0,"len":0,"gelecekteAlan":true}`)
		if err != nil {
			t.Fatal(err)
		}
		if msg != (End{ID: 1}) {
			t.Errorf("got %#v", msg)
		}
	})

	t.Run("end counters", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"end","id":2,"frames":6,"len":90000}`)
		if err != nil {
			t.Fatal(err)
		}
		if msg != (End{ID: 2, Frames: 6, Len: 90000}) {
			t.Errorf("got %#v", msg)
		}
	})
}

func TestCanonicalDetails(t *testing.T) {
	t.Run("headers are sorted", func(t *testing.T) {
		m := Req{ID: 1, Method: "GET", Path: "/", HasBody: false,
			Headers: map[string]string{"z": "3", "a": "1", "m": "2"}}
		want := `{"t":"req","id":1,"method":"GET","path":"/","headers":{"a":"1","m":"2","z":"3"},"hasBody":false}`
		if got := m.Canonical(); got != want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("empty headers and hasBody false are written", func(t *testing.T) {
		m := Req{ID: 0, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false}
		want := `{"t":"req","id":0,"method":"GET","path":"/","headers":{},"hasBody":false}`
		if got := m.Canonical(); got != want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("nil headers are written as an empty object", func(t *testing.T) {
		m := Res{ID: 1, Status: 200, Headers: nil}
		want := `{"t":"res","id":1,"status":200,"headers":{}}`
		if got := m.Canonical(); got != want {
			t.Errorf("got %s", got)
		}
	})

	// The same behaviour as JS JSON.stringify: HTML characters are not
	// escaped.
	t.Run("HTML characters are not escaped", func(t *testing.T) {
		m := Abort{ID: 1, Reason: "a<b>&c"}
		want := `{"t":"abort","id":1,"reason":"a<b>&c"}`
		if got := m.Canonical(); got != want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("non-ASCII characters are not escaped", func(t *testing.T) {
		m := Abort{ID: 1, Reason: "é✓ß"}
		want := `{"t":"abort","id":1,"reason":"é✓ß"}`
		if got := m.Canonical(); got != want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("control characters are escaped like JS", func(t *testing.T) {
		cases := map[string]string{
			"\x00":   `{"t":"abort","id":1,"reason":"\u0000"}`,
			"\x08":   `{"t":"abort","id":1,"reason":"\b"}`,
			"\t":     `{"t":"abort","id":1,"reason":"\t"}`,
			"\n":     `{"t":"abort","id":1,"reason":"\n"}`,
			"\x0c":   `{"t":"abort","id":1,"reason":"\f"}`,
			"\r":     `{"t":"abort","id":1,"reason":"\r"}`,
			"\x1f":   `{"t":"abort","id":1,"reason":"\u001f"}`,
			"\u007f": `{"t":"abort","id":1,"reason":"` + "\u007f" + `"}`,
			`"`:      `{"t":"abort","id":1,"reason":"\""}`,
			`\`:      `{"t":"abort","id":1,"reason":"\\"}`,
		}
		for in, want := range cases {
			if got := (Abort{ID: 1, Reason: in}).Canonical(); got != want {
				t.Errorf("reason %q: got %s, expected %s", in, got, want)
			}
		}
	})
}

func TestParseCtrlErrorOrdering(t *testing.T) {
	// The order in protocol/README.md: bad-json -> missing-field (no t)
	// -> bad-field (t is not a string) -> unknown-type.
	cases := []struct {
		text string
		want CtrlErrorCode
	}{
		{`not json`, CodeBadJSON},
		{`{"id":1}`, CodeMissingField},
		{`{"t":7,"id":1}`, CodeBadField},
		{`{"t":"missing","id":1}`, CodeUnknownType},
	}
	for _, c := range cases {
		_, err := ParseCtrl(c.text)
		if err == nil {
			t.Errorf("%s: an error was expected", c.text)
			continue
		}
		if got := ctrlCode(t, err); got != c.want {
			t.Errorf("%s: code %q, expected %q", c.text, got, c.want)
		}
	}
}
