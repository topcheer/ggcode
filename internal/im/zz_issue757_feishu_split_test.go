package im

import (
	"strings"
	"testing"
)

func TestSplitFeishuMessage_CJKChunksAreByteBounded(t *testing.T) {

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

func TestSplitFeishuMessage_ASCIIUnchanged(t *testing.T) {
	text := strings.Repeat("a", 28000)
	chunks := splitFeishuMessage(text, 28000)
	if len(chunks) != 1 {
		t.Fatalf("28000-byte ASCII must stay one chunk, got %d", len(chunks))
	}
}

func TestByteLimitPlatforms_FeishuRegistered(t *testing.T) {
	if !ByteLimitPlatforms[PlatformFeishu] {
		t.Fatal("PlatformFeishu must be in ByteLimitPlatforms (#757)")
	}

	text := strings.Repeat("字", 20000)
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
