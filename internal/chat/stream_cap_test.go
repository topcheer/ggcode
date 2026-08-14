package chat

import (
	"strings"
	"testing"
)

func TestCapStreamingBlockSmallPassthrough(t *testing.T) {
	block := "```go\nfmt.Println(1)\n```"
	if got := capStreamingBlock(block); got != block {
		t.Fatalf("small block must pass through unchanged, got %q", got)
	}
}

func TestCapStreamingBlockBounded(t *testing.T) {
	lines := make([]string, maxStreamingBlockLines+250)
	for i := range lines {
		lines[i] = "paragraph line"
	}
	got := capStreamingBlock(strings.Join(lines, "\n"))
	if !strings.Contains(got, "250 earlier lines hidden while streaming") {
		t.Fatalf("expected truncation marker, got prefix %q", firstLine(got))
	}
	// Output must be bounded: marker(1) + blank(1) + kept(maxStreamingBlockLines).
	if n := strings.Count(got, "\n"); n > maxStreamingBlockLines+2 {
		t.Fatalf("capped block too tall: %d lines", n+1)
	}
	// The NEWEST lines survive; the oldest are dropped.
	if !strings.HasSuffix(got, "paragraph line") {
		t.Fatal("expected kept tail to end with the newest line")
	}
}

func TestCapStreamingBlockRestoresFenceBalance(t *testing.T) {
	// A long fenced code block: truncation must not leave an orphan closing
	// fence that would open a never-closed block downstream.
	var sb strings.Builder
	sb.WriteString("```go\n")
	for i := 0; i < maxStreamingBlockLines+100; i++ {
		sb.WriteString("code()\n")
	}
	sb.WriteString("```")
	got := capStreamingBlock(sb.String())

	if missingOpenFence(strings.Split(got, "\n")) != "" {
		t.Fatal("capped fenced block must be fence-balanced")
	}
	// The re-opened fence must appear right after the marker so the kept
	// code content renders as code, not as loose paragraphs.
	markerEnd := strings.Index(got, "\n\n")
	if markerEnd < 0 || !strings.HasPrefix(got[markerEnd+2:], "```") {
		t.Fatalf("expected reopened fence after marker, got %q", firstLine(got[markerEnd+3:]))
	}
}

func TestRenderStreamingMarkdownCappedTailStillReusesEarlierBlocks(t *testing.T) {
	// Earlier (non-tail) blocks must still hit the block cache when only
	// the tail grows — the cap must not break incremental reuse.
	base := "first paragraph\n\nsecond paragraph\n\n"
	cache := streamRenderCache{}
	_, cache = renderStreamingMarkdown(base+"short tail", 80, &cache)

	long := strings.Repeat("line\n", maxStreamingBlockLines+10)
	_, next := renderStreamingMarkdown(base+long, 80, &cache)

	reuse := 0
	fresh := splitMarkdownBlocks(normalizeStreamingMarkdown(base + long))
	for reuse < len(cache.blocks) && reuse < len(fresh) && cache.blocks[reuse] == fresh[reuse] {
		reuse++
	}
	if reuse < 2 {
		t.Fatalf("expected first two blocks reused, got %d of %d", reuse, len(fresh))
	}
	_ = next
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
