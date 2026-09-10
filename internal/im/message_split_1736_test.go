package im

import (
	"strings"
	"testing"
)

// #1736 case 1: the #1553-C regression test's data had NO newline, so
// preferredByteSplit always returned the maximal prefix and the INNER
// flush (the fixed path) was never reached - its assertions passed
// identically on the pre-fix code. This case FORCES the inner flush:
// an early newline makes the preferred split short (start lags far
// behind i), then near-budget CJK fill makes the re-accumulated span
// exceed maxBytes inside the re-sync loop.
func TestSplitMessageBytesInnerFlushReachable1736(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("a\n") // early newline -> preferred split returns 1
	for i := 0; i < 30; i++ {
		sb.WriteString("中文段落") // 12B runes; re-sync span crosses maxBytes inside the inner loop
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
