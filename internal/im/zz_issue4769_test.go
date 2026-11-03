package im

import (
	"strings"
	"testing"
)

func TestIssue4769_MatrixChunkMarkdownByteBudget(t *testing.T) {

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

func TestIssue4769_MatrixChunkMarkdownTerminates(t *testing.T) {
	out := matrixChunkMarkdown(strings.Repeat("中", 5))
	if len(out) == 0 || strings.Join(out, "") == "" {
		t.Fatal("degenerate input must still yield chunks")
	}
}
