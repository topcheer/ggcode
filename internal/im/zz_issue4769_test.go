package im

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #4769-M2: SplitMarkdownBytes must keep every chunk within the BYTE budget
// regardless of content width - the rune-counted predecessor let 60K CJK
// runes (~180KB UTF-8) through against the ~100KB event limit.
func TestIssue4769_SplitMarkdownBytesByteBudget(t *testing.T) {
	const budget = 40000
	// Wide content: 50K CJK runes = 150KB UTF-8 - forces multiple chunks.
	wide := strings.Repeat("字", 50000)
	for _, chunk := range SplitMarkdownBytes(wide, budget) {
		if n := len(chunk); n > budget {
			t.Fatalf("chunk exceeds byte budget: %d > %d", n, budget)
		}
		if !utf8.ValidString(chunk) {
			t.Fatal("chunk is not valid UTF-8 (rune cap math split mid-rune)")
		}
	}

	// Narrow content also respects the budget.
	narrow := strings.Repeat("a", 50000)
	for _, chunk := range SplitMarkdownBytes(narrow, budget) {
		if n := len(chunk); n > budget {
			t.Fatalf("ASCII chunk exceeds byte budget: %d > %d", n, budget)
		}
	}

	// Short text passes through as a single chunk.
	got := SplitMarkdownBytes("hello", budget)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("short text should pass through, got %v", got)
	}
}

// #4769-M2: fence-awareness must survive the byte-budget path - a code block
// split across chunks is closed and reopened with its language tag.
func TestIssue4769_SplitMarkdownBytesKeepsFences(t *testing.T) {
	const budget = 400
	text := "before\n```go\n" + strings.Repeat("x = x + 1\n", 300) + "```\nafter"
	chunks := SplitMarkdownBytes(text, budget)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	opens, closes := 0, 0
	for _, c := range chunks {
		opens += strings.Count(c, "```go")
		closes += strings.Count(c, "\n```")
	}
	if opens < 2 || closes < 2 {
		t.Fatalf("fence markers not preserved across the split: opens=%d closes=%d", opens, closes)
	}
}
