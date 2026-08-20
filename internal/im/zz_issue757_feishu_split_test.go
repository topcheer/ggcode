package im

import (
	"strings"
	"testing"
)

// Regression guard for #757: Feishu chunks are measured in BYTES (card limit
// ~30KB JSON), not runes. The old rune split let 28000 CJK runes (~84KB)
// through, so every long Chinese reply failed the card API and degraded to
// plain text.
func TestSplitFeishuMessage_CJKChunksAreByteBounded(t *testing.T) {
	// 30000 CJK chars = 90000 bytes: must split into byte-bounded chunks.
	text := strings.Repeat("汉", 30000)
	chunks := splitFeishuMessage(text, 28000)
	if len(chunks) < 3 {
		t.Fatalf("90000-byte CJK text must split into >=3 chunks under a 28000-byte cap, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len(c); n > 28000 {
			t.Fatalf("chunk %d is %d bytes, exceeds 28000-byte card cap", i, n)
		}
	}
}

// ASCII behavior is preserved: 28000 ASCII chars = 28000 bytes, single chunk.
func TestSplitFeishuMessage_ASCIIUnchanged(t *testing.T) {
	text := strings.Repeat("a", 28000)
	chunks := splitFeishuMessage(text, 28000)
	if len(chunks) != 1 {
		t.Fatalf("28000-byte ASCII must stay one chunk, got %d", len(chunks))
	}
}

// Platform registry consistency: Feishu must be byte-measured everywhere.
func TestByteLimitPlatforms_FeishuRegistered(t *testing.T) {
	if !ByteLimitPlatforms[PlatformFeishu] {
		t.Fatal("PlatformFeishu must be in ByteLimitPlatforms (#757)")
	}
	// Round-trip via the generic path with a CJK payload.
	text := strings.Repeat("字", 20000) // 60000 bytes
	chunks := SplitMessageForPlatform(text, PlatformFeishu)
	if len(chunks) < 2 {
		t.Fatalf("generic path must also byte-split Feishu CJK, got %d chunks", len(chunks))
	}
	for i, c := range chunks {
		if n := len(c); n > 28000 {
			t.Fatalf("chunk %d is %d bytes, exceeds cap", i, n)
		}
	}
}
