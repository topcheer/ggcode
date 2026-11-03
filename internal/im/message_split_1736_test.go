package im

import (
	"strings"
	"testing"
)

func TestSplitMessageBytesInnerFlushReachable1736(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("a\n")
	for i := 0; i < 30; i++ {
		sb.WriteString("中文段落")
	}
	const maxBytes = 50
	chunks := splitMessageBytes(sb.String(), maxBytes, true)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for idx, c := range chunks {
		if got := len(c); got > maxBytes {
			t.Fatalf("chunk %d = %d bytes > max %d", idx, got, maxBytes)
		}
	}
	var total int
	for _, c := range chunks {
		total += len([]rune(c))
	}
	if total != len([]rune(sb.String())) {
		t.Fatalf("rune loss: %d vs %d", total, len([]rune(sb.String())))
	}
}
