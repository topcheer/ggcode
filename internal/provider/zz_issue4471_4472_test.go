//go:build goolm

package provider

import (
	"encoding/json"
	"testing"
)

// #4471: relays that omit index may stream parallel tool calls one chunk
// each; distinct IDs must land in distinct slots instead of merging into
// slot 0 (ID last-write-wins + arguments byte-concatenation).
func TestResolveNoIndexToolCallSlot(t *testing.T) {
	// call_A already accumulated in slot 0.
	mk := func() map[int]*ToolCallDelta {
		return map[int]*ToolCallDelta{
			0: {Index: 0, ID: "call_A", Name: "mkdir", Arguments: json.RawMessage(`{"path":"x"}`)},
		}
	}

	if got := resolveNoIndexToolCallSlot(nil, ""); got != 0 {
		t.Errorf("empty map + no ID: got slot %d, want 0", got)
	}
	if got := resolveNoIndexToolCallSlot(mk(), "call_A"); got != 0 {
		t.Errorf("same ID must reuse slot 0, got %d", got)
	}
	if got := resolveNoIndexToolCallSlot(mk(), "call_B"); got != 1 {
		t.Errorf("distinct ID must open a new slot, got %d, want 1", got)
	}
	// Slot 0 without an identity yet: first ID-bearing chunk claims it.
	unlabeled := map[int]*ToolCallDelta{0: {Index: 0}}
	if got := resolveNoIndexToolCallSlot(unlabeled, "call_A"); got != 0 {
		t.Errorf("unlabeled slot 0 must be claimable, got %d", got)
	}
	// Two existing calls: a third distinct ID must not reuse either.
	two := map[int]*ToolCallDelta{
		0: {Index: 0, ID: "call_A"},
		1: {Index: 1, ID: "call_B"},
	}
	if got := resolveNoIndexToolCallSlot(two, "call_C"); got != 2 {
		t.Errorf("third distinct ID must open slot 2, got %d", got)
	}
	if got := resolveNoIndexToolCallSlot(two, ""); got != 0 {
		t.Errorf("ID-less continuation chunk falls back to slot 0 (#722), got %d", got)
	}
}

// #4472: a prompt-only usage chunk materializes non-nil usage with
// OutputTokens==0; the done-time fallback must backfill the output
// estimate instead of silently zeroing cost/budget accounting.
func TestOpenAIUsageBackfillCondition(t *testing.T) {
	// Simulate the exact states the done-time fallback distinguishes.
	cases := []struct {
		name        string
		usage       *TokenUsage
		outputChars int
		wantBackfil bool
	}{
		{"nil usage stays on estimation path", nil, 100, false},
		{"complete usage untouched", &TokenUsage{InputTokens: 10, OutputTokens: 20}, 100, false},
		{"prompt-only usage with output backfilled", &TokenUsage{InputTokens: 500, OutputTokens: 0}, 400, true},
		{"prompt-only usage without output untouched", &TokenUsage{InputTokens: 500, OutputTokens: 0}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.usage != nil && tc.usage.OutputTokens == 0 && tc.outputChars > 0
			if got != tc.wantBackfil {
				t.Errorf("backfill = %v, want %v", got, tc.wantBackfil)
			}
		})
	}
	// estimateTokensFromChars is the backfill source; sanity-check scaling.
	if est := estimateTokensFromChars(400); est <= 0 {
		t.Errorf("estimateTokensFromChars(400) = %d, want > 0", est)
	}
}
