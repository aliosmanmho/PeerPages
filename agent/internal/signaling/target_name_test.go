package signaling_test

import (
	"testing"

	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// TestTargetNameRulesMatchTargetPackage shows the two name validators
// accept the same set.
//
// Why there are two: the signaling package knows only the message format
// (part of the protocol), while the target package resolves the targets
// coming from the CLI. They do not import each other, so the layers stay
// separate — but if their rules drift apart, a name from a URL passes
// signaling and blows up in target (or the other way around). This test
// catches exactly that drift.
func TestTargetNameRulesMatchTargetPackage(t *testing.T) {
	names := []string{
		"default", "admin", "a", "A", "0", "web-2_eski",
		"a-b-c", "ABC_123",
		"a123456789012345678901234567890b",  // 32: exactly at the limit
		"a1234567890123456789012345678901b", // 33: asiyor
		"", "-admin", "_admin", "a/b", "a.b", "a b", "x-é",
	}

	for _, name := range names {
		// What target.Parse accepts: does the "name=url" form produce a
		// valid Set.
		_, err := target.Parse([]string{name + "=http://localhost:3000"})
		targetOK := err == nil
		signalingOK := signaling.ValidTargetName(name)

		if targetOK != signalingOK {
			t.Errorf("%q: target accepts=%v, signaling accepts=%v (the rules have drifted apart)",
				name, targetOK, signalingOK)
		}
	}
}

// TestTargetNameNotTrimmed shows the signaling side does NOT TRIM the
// whitespace around a name.
//
// The target package does trim for a CLI `--target " admin "=url` (a
// convenience for the user). On the wire there is no trimming: otherwise
// "admin" and " admin" would resolve to the same target and the canonical
// form would become ambiguous.
func TestTargetNameNotTrimmed(t *testing.T) {
	for _, name := range []string{" admin", "admin ", "\tadmin", "admin\n"} {
		if signaling.ValidTargetName(name) {
			t.Errorf("%q was accepted; whitespace must not be trimmed on the wire", name)
		}
	}
}
