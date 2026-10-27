package protocol

import "fmt"

// WebSocket koprusunun cerceve alt bicimi (Faz 6).
//
// Dis cerceve ([4B id][4B seq][payload]) AYNEN korunur: ws yuku ayri bir
// kanal ya da ayri bir framing kullanmaz. Bir ws id'sine ait cercevenin
// PAYLOAD'i su bicimdedir:
//
//	[1B bayrak][parca]
//
// Boylece backpressure (internal/tunnel/flow.go), seq denetimi ve 16 KiB
// parcalama bedavaya gelir; ikili veri base64 sismesi yemez ve toplu ws
// trafigi ctrl kanalini (ping, abort) tikamaz.
//
// Normatif tanim: protocol/README.md.
const (
	// WSFlagBinary 1 ise ikili, 0 ise metin mesaji.
	WSFlagBinary byte = 0x01
	// WSFlagFin 1 ise bu, ws mesajinin SON parcasi.
	WSFlagFin byte = 0x02
	// WSFlagKnown tanimli bitler; disindaki bitler protokol hatasi.
	WSFlagKnown byte = WSFlagBinary | WSFlagFin

	// WSHeaderBytes ws alt basligi: yalnizca bayrak bayti.
	WSHeaderBytes = 1
	// MaxWSPayload bir ws parcasinin en buyuk boyu.
	MaxWSPayload = MaxPayload - WSHeaderBytes
)

// WSFrameErrorCode iki dilde ayni olan ws cerceve hata kodlari.
type WSFrameErrorCode string

const (
	// CodeWSEmptyFrame ws cercevesinin payload'i bos (bayrak bayti yok).
	CodeWSEmptyFrame WSFrameErrorCode = "ws-empty-frame"
	// CodeWSBadFlags bayrak baytinda tanimsiz bit var.
	CodeWSBadFlags WSFrameErrorCode = "ws-bad-flags"
	// CodeWSPayloadTooLarge parca MaxWSPayload sinirini asiyor.
	CodeWSPayloadTooLarge WSFrameErrorCode = "ws-payload-too-large"
)

// WSFrameError ws cerceve hatasi. Code degeri testvectors.json ile eslesir.
type WSFrameError struct {
	Code   WSFrameErrorCode
	Detail string
}

func (e *WSFrameError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// WSChunk cozulmus bir ws parcasi.
type WSChunk struct {
	// Binary mesajin ikili olup olmadigi. Bir mesajin TUM parcalarinda ayni
	// olmak zorunda.
	Binary bool
	// Fin bu parca mesajin sonu mu.
	Fin bool
	// Payload parcanin kendisi (bos olabilir: bos ws mesaji tek bayraktir).
	Payload []byte
}

// EncodeWSPayload ws parcasini dis cercevenin payload'ina cevirir.
func EncodeWSPayload(c WSChunk) ([]byte, error) {
	if len(c.Payload) > MaxWSPayload {
		return nil, &WSFrameError{
			Code:   CodeWSPayloadTooLarge,
			Detail: fmt.Sprintf("%d bayt, sinir %d", len(c.Payload), MaxWSPayload),
		}
	}
	out := make([]byte, WSHeaderBytes+len(c.Payload))
	out[0] = wsFlags(c)
	copy(out[WSHeaderBytes:], c.Payload)
	return out, nil
}

// AppendWSPayload ws parcasini dst'ye ekler; sicak yolda tahsisi azaltmak icin
// (AppendFrame ile ayni gerekce).
func AppendWSPayload(dst []byte, c WSChunk) ([]byte, error) {
	if len(c.Payload) > MaxWSPayload {
		return dst, &WSFrameError{
			Code:   CodeWSPayloadTooLarge,
			Detail: fmt.Sprintf("%d bayt, sinir %d", len(c.Payload), MaxWSPayload),
		}
	}
	dst = append(dst, wsFlags(c))
	return append(dst, c.Payload...), nil
}

func wsFlags(c WSChunk) byte {
	var f byte
	if c.Binary {
		f |= WSFlagBinary
	}
	if c.Fin {
		f |= WSFlagFin
	}
	return f
}

// DecodeWSPayload dis cercevenin payload'ini ws parcasina cozer.
//
// Donen Payload, verilen dilimin KOPYASI DEGILDIR: DecodeFrame zaten kopya
// uretiyor, ikinci kopya gereksiz olurdu. Cagiran taraf dilimi saklayacaksa
// kendisi kopyalar.
func DecodeWSPayload(payload []byte) (WSChunk, error) {
	if len(payload) < WSHeaderBytes {
		return WSChunk{}, &WSFrameError{
			Code:   CodeWSEmptyFrame,
			Detail: "ws cercevesinin payload'i en az 1 bayt olmali (bayrak)",
		}
	}
	flags := payload[0]
	if flags&^WSFlagKnown != 0 {
		return WSChunk{}, &WSFrameError{
			Code:   CodeWSBadFlags,
			Detail: fmt.Sprintf("tanimsiz bit: 0x%02x", flags),
		}
	}
	return WSChunk{
		Binary:  flags&WSFlagBinary != 0,
		Fin:     flags&WSFlagFin != 0,
		Payload: payload[WSHeaderBytes:],
	}, nil
}
