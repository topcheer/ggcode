package im

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// TestNormalizeNostrKeyHexOnlyForDisplayHelpers pins the #2626 invariant:
// go-nostr display-side helpers (GetPublicKey, nip19.EncodePrivateKey)
// accept hex only, so an nsec1-format user key must be normalized before
// being fed to them. The pre-fix TUI display path called them with the
// raw nsec string, swallowed the error, and rendered an empty npub plus
// a QR code of just "nostr:" while still reporting success.
func TestNormalizeNostrKeyHexOnlyForDisplayHelpers(t *testing.T) {
	skHex := nostr.GeneratePrivateKey()
	nsec, err := nip19.EncodePrivateKey(skHex)
	if err != nil {
		t.Fatalf("encode private key: %v", err)
	}
	if !strings.HasPrefix(nsec, "nsec1") {
		t.Fatalf("expected nsec1 prefix, got %q", nsec)
	}

	// Raw nsec fails on the display helpers (the pre-fix behavior).
	if _, err := nostr.GetPublicKey(nsec); err == nil {
		t.Fatal("expected GetPublicKey to reject raw nsec input; if go-nostr now accepts it, this test and the fix need reevaluation")
	}

	// Normalized form must be the hex key and must work on every helper.
	got := NormalizeNostrKey(nsec)
	if got != skHex {
		t.Fatalf("NormalizeNostrKey(nsec) = %q, want original hex %q", got, skHex)
	}
	pub, err := nostr.GetPublicKey(got)
	if err != nil {
		t.Fatalf("GetPublicKey(normalized): %v", err)
	}
	if pub == "" {
		t.Fatal("GetPublicKey(normalized) returned empty pubkey")
	}
	npub, err := nip19.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey: %v", err)
	}
	if !strings.HasPrefix(npub, "npub1") || strings.HasSuffix(npub, "\x00") {
		t.Fatalf("unexpected npub %q", npub)
	}
	back, err := nip19.EncodePrivateKey(got)
	if err != nil {
		t.Fatalf("EncodePrivateKey: %v", err)
	}
	if back != nsec {
		t.Fatalf("EncodePrivateKey(normalized) = %q, want %q", back, nsec)
	}
}

// TestNormalizeNostrKeyPassthrough covers the non-nsec branches: hex
// input lowercases to itself; junk input round-trips unchanged so the
// validator (not the normalizer) is what rejects it.
func TestNormalizeNostrKeyPassthrough(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	if got := NormalizeNostrKey(sk); got != sk {
		t.Fatalf("hex passthrough = %q, want %q", got, sk)
	}
	if got := NormalizeNostrKey("UPPER" + sk[5:]); got != strings.ToLower("UPPER"+sk[5:]) {
		t.Fatalf("uppercase hex should lowercase, got %q", got)
	}
	if got := NormalizeNostrKey("nsec1garbage!!"); got != "nsec1garbage!!" {
		t.Fatalf("undecodable nsec should pass through unchanged, got %q", got)
	}
}
