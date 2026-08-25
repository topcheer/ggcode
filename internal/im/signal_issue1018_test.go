package im

import (
	"strings"
	"testing"
)

// TestSignalResolveImageDataURLNoSemicolon pins #1018 defect 1: an RFC 2397
// data URL without a ";" parameter (URL-encoded data, not base64) must not
// panic in the data_url branch. It may fail cleanly (base64 decode error on
// URL-encoded data) or succeed, but never crash.
func TestSignalResolveImageDataURLNoSemicolon(t *testing.T) {
	a := &signalAdapter{}
	_, _, _, err := a.resolveImageBytes(t.Context(), ExtractedImage{
		Kind: "data_url",
		Data: "data:image/png,%89PNG%0D%0A", // no ";base64" marker
	})
	// The critical assertion is that we got here without panicking; either a
	// clean error (base64 decode of URL-encoded data fails) or success with a
	// sane mime is acceptable.
	if err != nil && !strings.Contains(err.Error(), "decode data URL") {
		t.Fatalf("unexpected error shape: %v", err)
	}
}

// TestSignalResolveImageDataURLWithParams keeps the normal path pinned.
func TestSignalResolveImageDataURLWithParams(t *testing.T) {
	a := &signalAdapter{}
	data, mime, name, err := a.resolveImageBytes(t.Context(), ExtractedImage{
		Kind: "data_url",
		Data: "data:image/jpeg;base64,AAECAw==",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if mime != "image/jpeg" || name != "image.jpg" || len(data) != 4 {
		t.Fatalf("mime=%q name=%q len=%d", mime, name, len(data))
	}
}

// TestSignalMentionsAccount pins #1018 defect 2's helper: structured mention
// arrays carry the E.164 number, which mention gating must consult.
func TestSignalMentionsAccount(t *testing.T) {
	mentions := []any{
		map[string]any{"name": "Alice", "number": "+15550001111"},
		map[string]any{"name": "Bot", "number": "+15551234567"},
	}
	if !signalMentionsAccount(mentions, "+15551234567") {
		t.Fatal("expected structured mention of bot account to match")
	}
	if signalMentionsAccount(mentions, "+19999999999") {
		t.Fatal("unrelated number must not match")
	}
	if signalMentionsAccount(nil, "+15551234567") {
		t.Fatal("nil mentions must not match")
	}
	if signalMentionsAccount([]any{"not-a-map"}, "+15551234567") {
		t.Fatal("malformed entry must not match")
	}
	if signalMentionsAccount(mentions, "") {
		t.Fatal("empty account must not match")
	}
}
