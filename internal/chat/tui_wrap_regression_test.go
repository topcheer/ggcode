package chat

import (
	"strings"
	"testing"
)

func TestRenderStreamingMarkdownFenceCacheHit(t *testing.T) {
	chunk1 := "```go\nfoo"
	chunk2 := "```go\nfoobar"

	out1, cache := renderStreamingMarkdown(chunk1, 80, nil)
	if out1 == "" {
		t.Fatal("expected non-empty render for chunk1")
	}

	_, cache2 := renderStreamingMarkdown(chunk2, 80, &cache)

	if !strings.HasPrefix(chunk2, strings.TrimSuffix(cache.source, "")) {

		t.Fatalf("cache source %q is not a compatible prefix of chunk2 %q", cache.source, chunk2)
	}
	if cache2.width != 80 {
		t.Fatalf("expected width 80, got %d", cache2.width)
	}
}

type reuseProbe struct{}

func TestRenderStreamingMarkdownClosedBlockReusedWhileFenceGrows(t *testing.T) {
	chunk1 := "# Title\n\nfirst paragraph text\n\n```go\nfoo"
	chunk2 := "# Title\n\nfirst paragraph text\n\n```go\nfoobar"

	_, cache := renderStreamingMarkdown(chunk1, 80, nil)
	out2, cache2 := renderStreamingMarkdown(chunk2, 80, &cache)

	if out2 == "" {
		t.Fatal("expected non-empty render for chunk2")
	}

	if cache2.rendered[0] != cache.rendered[0] {
		t.Fatalf("stable first block was not reused from cache")
	}
}

func TestRenderGitLogWrapsLongLines(t *testing.T) {
	long := strings.Repeat("fix: very long commit subject line that exceeds the terminal width ", 3)
	item := &BaseToolItem{fileBodyMode: "gitlog"}
	item.SetResult(long, false)
	item.styles = DefaultStyles()

	const width = 60
	body := item.RenderBody(width)
	for _, line := range strings.Split(body, "\n") {

		plain := stripANSI(line)
		if plain != "" && len([]rune(plain)) > width {
			t.Fatalf("renderGitLog produced a %d-rune line exceeding width %d: %q", len([]rune(plain)), width, plain)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
