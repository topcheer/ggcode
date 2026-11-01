package im

import (
	"strings"
	"testing"
)

// #4769 defect 1: matrix chunking must budget BYTES (Body+FormattedBody
// pair) against the ~100KB Synapse event ceiling, not runes - a CJK-heavy
// 60K-rune chunk is ~180KB and M_TOO_LARGE fell through to a hard error
// after partial delivery.
func TestIssue4769_MatrixChunkMarkdownByteBudget(t *testing.T) {
	// CJK-heavy: every rune is 3 bytes.
	cjk := strings.Repeat("中", 90000)
	chunks := matrixChunkMarkdown(cjk)
	if len(chunks) < 2 {
		t.Fatalf("90K CJK runes must split, got %d chunks", len(chunks))
	}
	for i, c := range chunks {
		if n := len(c); n*2+2048 > matrixMaxEventByteBudget {
			t.Fatalf("chunk %d exceeds byte budget: %d bytes (pair %d+2048 vs %d)", i, n, n*2, matrixMaxEventByteBudget)
		}
	}
	if got := strings.Count(strings.Join(chunks, ""), "中"); got != 90000 {
		t.Fatalf("content loss: %d/90000 runes survived", got)
	}
}

// Convergence: ASCII text also lands under the byte budget after the
// proportional shrink (60K-rune chunks at 2x+2048 exceed 95KB, so the first
// round shrinks; later rounds converge), with zero content loss.
func TestIssue4769_MatrixChunkMarkdownAsciiConverges(t *testing.T) {
	ascii := strings.Repeat("a", 200000)
	chunks := matrixChunkMarkdown(ascii)
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	for i, c := range chunks {
		if n := len(c); n*2+2048 > matrixMaxEventByteBudget {
			t.Fatalf("ascii chunk %d exceeds byte budget: %d", i, n)
		}
	}
	if got := strings.Count(strings.Join(chunks, ""), "a"); got != 200000 {
		t.Fatalf("content loss: %d/200000", got)
	}
}

// Termination: degenerate input still yields chunks (8-round bound and the
// 1000-rune floor guarantee no infinite loop).
func TestIssue4769_MatrixChunkMarkdownTerminates(t *testing.T) {
	out := matrixChunkMarkdown(strings.Repeat("中", 5))
	if len(out) == 0 || strings.Join(out, "") == "" {
		t.Fatal("degenerate input must still yield chunks")
	}
}
