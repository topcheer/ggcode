package im

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr/nip19"
)

// #2626: the TUI panel feeds the user-provided private key to go-nostr's
// hex-only helpers after normalizing via this export. A valid nsec1 must
// round-trip to 64-char hex that GetPublicKey accepts; hex input passes
// through unchanged; garbage stays garbage (validation rejects it earlier).
func TestNormalizeNostrPrivateKeyExport(t *testing.T) {
	// Build a valid nsec from a known hex key.
	hexKey := strings.Repeat("ab", 32)
	nsec, err := nip19.EncodePrivateKey(hexKey)
	if err != nil {
		t.Fatalf("setup: EncodePrivateKey: %v", err)
	}
	if !strings.HasPrefix(nsec, "nsec1") {
		t.Fatalf("setup: expected nsec1 prefix, got %q", nsec)
	}

	if got := NormalizeNostrPrivateKey(nsec); got != hexKey {
		t.Fatalf("nsec input must normalize to hex %q, got %q", hexKey, got)
	}
	if got := NormalizeNostrPrivateKey("  " + hexKey + " "); got != hexKey {
		t.Fatalf("hex input with whitespace must pass through trimmed, got %q", got)
	}
	if got := NormalizeNostrPrivateKey("not-a-key"); got != "not-a-key" {
		t.Fatalf("garbage must pass through unchanged (validation rejects it), got %q", got)
	}
}
