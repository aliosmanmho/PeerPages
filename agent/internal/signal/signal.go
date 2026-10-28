// Package signal implements the SDP transport format for manual
// (copy-paste) signaling.
//
// In phase 1 there is NO signaling server and no Nostr: the agent prints
// the offer to stdout as a single line, the user pastes it into the
// visitor page, and the answer the page produces is pasted into the
// agent's stdin. The point was to prove the SW + WebRTC + reverse proxy
// chain (BRIEF phase 1).
//
// The format: base64(JSON(RTCSessionDescription)). One line, easy to
// copy. TypeScript counterpart: web/src/signal.ts.
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

// MaxEncodedLen bounds the pasted text. With manual signaling the input
// comes from the user; we do not parse unbounded data.
const MaxEncodedLen = 256 * 1024

// Encode turns an SDP into a single line of base64.
func Encode(sd webrtc.SessionDescription) (string, error) {
	if sd.SDP == "" {
		return "", errors.New("the SDP is empty")
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		return "", fmt.Errorf("the SDP could not be serialized: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// Decode turns pasted text back into an SDP.
//
// Every whitespace character in the text (line breaks included) is
// ignored: a long line copied from a terminal may have been wrapped.
func Decode(pasted string) (webrtc.SessionDescription, error) {
	var zero webrtc.SessionDescription

	if len(pasted) > MaxEncodedLen {
		return zero, fmt.Errorf("the pasted text is too long: %d bytes (limit %d)", len(pasted), MaxEncodedLen)
	}

	clean := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, pasted)
	if clean == "" {
		return zero, errors.New("empty input")
	}

	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return zero, fmt.Errorf("base64 could not be decoded: %w", err)
	}

	var sd webrtc.SessionDescription
	if err := json.Unmarshal(raw, &sd); err != nil {
		return zero, fmt.Errorf("the SDP JSON could not be read: %w", err)
	}
	if sd.SDP == "" {
		return zero, errors.New("the SDP is empty")
	}
	switch sd.Type {
	case webrtc.SDPTypeOffer, webrtc.SDPTypeAnswer, webrtc.SDPTypePranswer, webrtc.SDPTypeRollback:
	default:
		return zero, fmt.Errorf("invalid SDP type: %q", sd.Type.String())
	}
	return sd, nil
}
