package im

import (
	"strings"
	"testing"
)

func TestSignalResolveImageDataURLNoSemicolon(t *testing.T) {
	a := &signalAdapter{}
	_, _, _, err := a.resolveImageBytes(t.Context(), ExtractedImage{
		Kind: "data_url",
		Data: "data:image/png,%89PNG%0D%0A",
	})

	if err != nil && !strings.Contains(err.Error(), "decode data URL") {
		t.Fatalf("unexpected error shape: %v", err)
	}
}

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

func TestSignalGateGroupMention(t *testing.T) {
	a := &signalAdapter{account: "+15551234567"}

	mentions := []any{map[string]any{"name": "Bot", "number": "+15551234567"}}
	text, drop := a.gateGroupMention("do the thing", map[string]any{"mentions": mentions})
	if drop || text != "do the thing" {
		t.Fatalf("structured mention: drop=%v text=%q", drop, text)
	}

	text, drop = a.gateGroupMention("hey +15551234567 run tests", map[string]any{})
	if drop || strings.Contains(text, "+15551234567") {
		t.Fatalf("text mention: drop=%v text=%q", drop, text)
	}

	_, drop = a.gateGroupMention("no mention here", map[string]any{})
	if !drop {
		t.Fatal("message without mention must be dropped")
	}
}
