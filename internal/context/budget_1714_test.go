package context

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestBudgetToolResultWithImages1714(t *testing.T) {
	textOnly := provider.ContentBlock{Type: "tool_result", Output: "abcdef"}
	withTwo := textOnly
	withTwo.Images = []provider.ContentImage{
		{MIME: "image/png", Base64: "aaaa"},
		{MIME: "image/png", Base64: "bbbb"},
	}
	base := AnalyzeBudget([]provider.Message{{Role: "user", Content: []provider.ContentBlock{textOnly}}})
	both := AnalyzeBudget([]provider.Message{{Role: "user", Content: []provider.ContentBlock{withTwo}}})
	if got := both.TotalTokens - base.TotalTokens; got != 600 {
		t.Fatalf("2 embedded images must add exactly 600 (2x300), got delta %d", got)
	}
}
