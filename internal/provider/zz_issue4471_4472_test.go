//go:build goolm

package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// #4471: parallel tool calls from index-omitting minimal relays arrive as
// consecutive single-call deltas; a changing tc.ID must allocate a FRESH
// slot so call_B does not merge into call_A's slot 0.
func TestIssue4471_NextImplicitToolCallSlotSplitsOnIDChange(t *testing.T) {
	cases := []struct {
		name      string
		prevID    string
		prevIdx   int
		id        string
		wantIdx   int
		wantCurID string
	}{
		{"first delta establishes slot 0", "", 0, "call_A", 0, "call_A"},
		{"same ID argument fragments stay", "call_A", 0, "call_A", 0, "call_A"},
		{"ID change allocates next slot", "call_A", 0, "call_B", 1, "call_B"},
		{"third call continues forward", "call_B", 1, "call_C", 2, "call_C"},
		{"empty ID keeps state (fragments)", "call_A", 0, "", 0, "call_A"},
		{"empty ID after two calls keeps slot", "call_B", 1, "", 1, "call_B"},
	}
	for _, c := range cases {
		idx, curID := nextImplicitToolCallSlot(c.prevID, c.prevIdx, c.id)
		if idx != c.wantIdx || curID != c.wantCurID {
			t.Errorf("%s: got (%d,%q), want (%d,%q)", c.name, idx, curID, c.wantIdx, c.wantCurID)
		}
	}
}

// #4472: a prompt-only usage chunk (input>0, output=0) must NOT suppress
// the output-side estimation while the stream produced output.
func TestIssue4472_UsageNeedsOutputEstimate(t *testing.T) {
	if !usageNeedsOutputEstimate(&TokenUsage{InputTokens: 500}, 120) {
		t.Error("prompt-only usage + real output must need the estimate")
	}
	if usageNeedsOutputEstimate(nil, 120) {
		t.Error("nil usage goes through the estimation branch already")
	}
	if usageNeedsOutputEstimate(&TokenUsage{OutputTokens: 7}, 120) {
		t.Error("usage with output side must be left alone")
	}
	if usageNeedsOutputEstimate(&TokenUsage{InputTokens: 500}, 0) {
		t.Error("no output chars means nothing to estimate")
	}
}

// #4472 loop-seam contract: applyOpenAIUsage must still materialize a
// prompt-only chunk (that behavior is correct - only the OUTPUT side was
// missing), and the fill above keys off exactly that shape.
func TestIssue4472_ApplyOpenAIUsageMaterializesPromptOnly(t *testing.T) {
	got := applyOpenAIUsage(nil, openai.Usage{PromptTokens: 500, CompletionTokens: 0})
	if got == nil || got.InputTokens != 500 || got.OutputTokens != 0 {
		t.Fatalf("prompt-only chunk must materialize (in=500,out=0), got %+v", got)
	}
	if !usageNeedsOutputEstimate(got, 200) {
		t.Error("materialized prompt-only usage + output chars must need the estimate")
	}
}
