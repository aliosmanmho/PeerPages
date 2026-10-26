package signal

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestRoundtrip(t *testing.T) {
	in := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=-\r\n",
	}
	enc, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode hata: %v", err)
	}
	if strings.ContainsAny(enc, " \t\r\n") {
		t.Errorf("kodlanmis metin tek satir olmali: %q", enc)
	}

	out, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode hata: %v", err)
	}
	if out.Type != in.Type || out.SDP != in.SDP {
		t.Errorf("roundtrip bozuldu: %#v", out)
	}
}

func TestDecodeToleratesWhitespace(t *testing.T) {
	enc, err := Encode(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: "v=0\r\n"})
	if err != nil {
		t.Fatal(err)
	}

	// Terminalden kopyalanan metin satirlara kirilmis ve bosluklu olabilir.
	mangled := "  " + enc[:10] + "\n" + enc[10:20] + "\r\n\t" + enc[20:] + "  \n"
	out, err := Decode(mangled)
	if err != nil {
		t.Fatalf("bosluklu girdi kabul edilmeliydi: %v", err)
	}
	if out.Type != webrtc.SDPTypeAnswer {
		t.Errorf("tip %q", out.Type)
	}
}

func TestEncodeRejectsEmptySDP(t *testing.T) {
	if _, err := Encode(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer}); err == nil {
		t.Error("bos SDP reddedilmeliydi")
	}
}

func TestDecodeRejects(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	cases := map[string]string{
		"bos":               "",
		"sadece bosluk":     "   \n\t ",
		"base64 degil":      "bu base64 degil!!!",
		"json degil":        b64("merhaba"),
		"json dizi":         b64(`["x"]`),
		"sdp alani yok":     b64(`{"type":"offer"}`),
		"sdp bos":           b64(`{"type":"offer","sdp":""}`),
		"gecersiz sdp tipi": b64(`{"type":"zipzip","sdp":"v=0"}`),
		"tip alani yok":     b64(`{"sdp":"v=0"}`),
		"cok uzun":          strings.Repeat("A", MaxEncodedLen+1),
	}
	for name, in := range cases {
		if got, err := Decode(in); err == nil {
			t.Errorf("%s: reddedilmeliydi, donen: %#v", name, got)
		}
	}
}

func TestDecodeAcceptsAllValidTypes(t *testing.T) {
	for _, typ := range []webrtc.SDPType{
		webrtc.SDPTypeOffer, webrtc.SDPTypeAnswer,
		webrtc.SDPTypePranswer, webrtc.SDPTypeRollback,
	} {
		enc, err := Encode(webrtc.SessionDescription{Type: typ, SDP: "v=0\r\n"})
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if _, err := Decode(enc); err != nil {
			t.Errorf("%s kabul edilmeliydi: %v", typ, err)
		}
	}
}
