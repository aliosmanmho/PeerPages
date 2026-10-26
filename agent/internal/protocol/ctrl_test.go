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
		t.Fatalf("CtrlError beklenirdi, geldi: %T (%v)", err, err)
	}
	return ce.Code
}

func TestCtrlValidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Ctrl.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := ParseCtrl(vec.Text)
			if err != nil {
				t.Fatalf("ParseCtrl hata: %v", err)
			}
			// Kanonik cikti TS tarafiyla bayt bayt ayni olmali.
			if got := msg.Canonical(); got != vec.Canonical {
				t.Errorf("kanonik cikti farkli:\n  geldi:    %s\n  beklenen: %s", got, vec.Canonical)
			}
			// Kanonik metin yeniden ayristirildiginda ayni mesaji vermeli.
			again, err := ParseCtrl(msg.Canonical())
			if err != nil {
				t.Fatalf("kanonik metin yeniden ayristirilamadi: %v", err)
			}
			if !reflect.DeepEqual(again, msg) {
				t.Errorf("roundtrip bozuldu:\n  %#v\n  %#v", again, msg)
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
	want := []string{"abort", "end", "hello", "ping", "pong", "req", "res"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tum tipler kapsanmali:\n  geldi:    %v\n  beklenen: %v", got, want)
	}
}

func TestCtrlInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Ctrl.Invalid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := ParseCtrl(vec.Text)
			if err == nil {
				t.Fatalf("hata beklenirdi (%s), donen: %#v", vec.Error, msg)
			}
			if code := ctrlCode(t, err); string(code) != vec.Error {
				t.Errorf("hata kodu %q, beklenen %q (detay: %v)", code, vec.Error, err)
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
			t.Errorf("geldi %#v", msg)
		}
	})

	t.Run("res", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"res","id":3,"status":404,"headers":{}}`)
		if err != nil {
			t.Fatal(err)
		}
		want := Res{ID: 3, Status: 404, Headers: map[string]string{}}
		if !reflect.DeepEqual(msg, want) {
			t.Errorf("geldi %#v", msg)
		}
	})

	t.Run("hello", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"hello","v":1,"targets":["default","admin"],"auth":"required"}`)
		if err != nil {
			t.Fatal(err)
		}
		want := Hello{V: 1, Targets: []string{"default", "admin"}, Auth: AuthRequired}
		if !reflect.DeepEqual(msg, want) {
			t.Errorf("geldi %#v", msg)
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

	t.Run("bilinmeyen fazladan alanlar yok sayilir", func(t *testing.T) {
		msg, err := ParseCtrl(`{"t":"end","id":1,"gelecekteAlan":true}`)
		if err != nil {
			t.Fatal(err)
		}
		if msg != (End{ID: 1}) {
			t.Errorf("geldi %#v", msg)
		}
	})
}

func TestCanonicalDetails(t *testing.T) {
	t.Run("headers siralanir", func(t *testing.T) {
		m := Req{ID: 1, Method: "GET", Path: "/", HasBody: false,
			Headers: map[string]string{"z": "3", "a": "1", "m": "2"}}
		want := `{"t":"req","id":1,"method":"GET","path":"/","headers":{"a":"1","m":"2","z":"3"},"hasBody":false}`
		if got := m.Canonical(); got != want {
			t.Errorf("geldi %s", got)
		}
	})

	t.Run("bos headers ve false hasBody yazilir", func(t *testing.T) {
		m := Req{ID: 0, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false}
		want := `{"t":"req","id":0,"method":"GET","path":"/","headers":{},"hasBody":false}`
		if got := m.Canonical(); got != want {
			t.Errorf("geldi %s", got)
		}
	})

	t.Run("nil headers bos nesne olarak yazilir", func(t *testing.T) {
		m := Res{ID: 1, Status: 200, Headers: nil}
		want := `{"t":"res","id":1,"status":200,"headers":{}}`
		if got := m.Canonical(); got != want {
			t.Errorf("geldi %s", got)
		}
	})

	// JS JSON.stringify ile ayni davranis: HTML karakterleri kacirilmaz.
	t.Run("HTML karakterleri kacirilmaz", func(t *testing.T) {
		m := Abort{ID: 1, Reason: "a<b>&c"}
		want := `{"t":"abort","id":1,"reason":"a<b>&c"}`
		if got := m.Canonical(); got != want {
			t.Errorf("geldi %s", got)
		}
	})

	t.Run("ASCII disi karakterler kacirilmaz", func(t *testing.T) {
		m := Abort{ID: 1, Reason: "çğü"}
		want := `{"t":"abort","id":1,"reason":"çğü"}`
		if got := m.Canonical(); got != want {
			t.Errorf("geldi %s", got)
		}
	})

	t.Run("kontrol karakterleri JS gibi kacirilir", func(t *testing.T) {
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
				t.Errorf("reason %q: geldi %s, beklenen %s", in, got, want)
			}
		}
	})
}

func TestParseCtrlErrorOrdering(t *testing.T) {
	// protocol/README.md'deki sira: bad-json -> missing-field (t yok) ->
	// bad-field (t string degil) -> unknown-type.
	cases := []struct {
		text string
		want CtrlErrorCode
	}{
		{`not json`, CodeBadJSON},
		{`{"id":1}`, CodeMissingField},
		{`{"t":7,"id":1}`, CodeBadField},
		{`{"t":"yok","id":1}`, CodeUnknownType},
	}
	for _, c := range cases {
		_, err := ParseCtrl(c.text)
		if err == nil {
			t.Errorf("%s: hata beklenirdi", c.text)
			continue
		}
		if got := ctrlCode(t, err); got != c.want {
			t.Errorf("%s: kod %q, beklenen %q", c.text, got, c.want)
		}
	}
}
