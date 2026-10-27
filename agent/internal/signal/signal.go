// Package signal elle (kopyala-yapistir) signaling icin SDP tasima bicimini
// uygular.
//
// Faz 1'de signaling sunucusu ve Nostr YOK: ajan offer'i tek satir olarak
// stdout'a basar, kullanici ziyaretci sayfasina yapistirir, sayfanin urettigi
// answer ajanin stdin'ine yapistirilir. Amac SW + WebRTC + reverse proxy
// zincirini kanitlamak (BRIEF Faz 1).
//
// Bicim: base64(JSON(RTCSessionDescription)). Tek satir, kopyalanabilir.
// TypeScript karsiligi: web/src/signal.ts.
package signal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/pion/webrtc/v4"
)

// MaxEncodedLen yapistirilan metin icin ust sinir. Elle signaling'de girdi
// kullanicidan gelir; sinirsiz veri ayristirmayiz.
const MaxEncodedLen = 256 * 1024

// Encode SDP'yi tek satirlik base64 metne cevirir.
func Encode(sd webrtc.SessionDescription) (string, error) {
	if sd.SDP == "" {
		return "", errors.New("SDP bos")
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		return "", fmt.Errorf("SDP serilestirilemedi: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// Decode yapistirilan metni SDP'ye cevirir.
//
// Metin icindeki tum bosluk karakterleri (satir sonlari dahil) yok sayilir:
// terminalden kopyalanan uzun satirlar kirilmis olabilir.
func Decode(pasted string) (webrtc.SessionDescription, error) {
	var zero webrtc.SessionDescription

	if len(pasted) > MaxEncodedLen {
		return zero, fmt.Errorf("yapistirilan metin cok uzun: %d bayt (sinir %d)", len(pasted), MaxEncodedLen)
	}

	clean := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, pasted)
	if clean == "" {
		return zero, errors.New("bos girdi")
	}

	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return zero, fmt.Errorf("base64 cozulemedi: %w", err)
	}

	var sd webrtc.SessionDescription
	if err := json.Unmarshal(raw, &sd); err != nil {
		return zero, fmt.Errorf("SDP JSON'u okunamadi: %w", err)
	}
	if sd.SDP == "" {
		return zero, errors.New("SDP bos")
	}
	switch sd.Type {
	case webrtc.SDPTypeOffer, webrtc.SDPTypeAnswer, webrtc.SDPTypePranswer, webrtc.SDPTypeRollback:
	default:
		return zero, fmt.Errorf("gecersiz SDP tipi: %q", sd.Type.String())
	}
	return sd, nil
}
