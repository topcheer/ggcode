package provider

// #4563: two usage undercounts in openai.go.
//   A) non-streaming prompt-only usage (PromptTokens>0, CompletionTokens==0)
//      passed through with OutputTokens==0 - the streaming path backfills
//      this (#4472), the non-streaming path did not.
//   B) applyOpenAIUsage materializes a completion-only chunk (Output>0,
//      Input==0), which then skipped both the usage==nil estimation
//      fallback and the #4472 output-backfill branch (it requires
//      Output==0) - InputTokens stayed 0 forever.

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// B precondition: a completion-only chunk materializes usage (nil cur) with
// InputTokens==0 - exactly the state whose input side must be backfillable.
func TestIssue4563_CompletionOnlyChunkMaterializes(t *testing.T) {
	got := applyOpenAIUsage(nil, openai.Usage{CompletionTokens: 42})
	if got == nil {
		t.Fatal("#4563 precondition: completion-only chunk must materialize usage (non-nil)")
	}
	if got.OutputTokens != 42 || got.InputTokens != 0 {
		t.Fatalf("materialized usage = %+v, want Output=42 Input=0", got)
	}
	// All-zero chunk still does not materialize (#1286 fallback preserved).
	if applyOpenAIUsage(nil, openai.Usage{}) != nil {
		t.Fatal("#1286 regression: all-zero chunk must not materialize")
	}
}

// The done-time backfill predicates: #4472 output-side and the new #4563
// input-side. Simulates the exact states the fallback distinguishes.
func TestIssue4563_BackfillPredicates(t *testing.T) {
	cases := []struct {
		name        string
		usage       *TokenUsage
		outputChars int
		wantOutput  bool // #4472: Output==0 && chars>0
		wantInput   bool // #4563: Input==0 (non-nil usage, post-materialize)
	}{
		{"nil usage stays on estimation path", nil, 100, false, false},
		{"complete usage untouched", &TokenUsage{InputTokens: 10, OutputTokens: 20}, 100, false, false},
		{"prompt-only usage output backfilled", &TokenUsage{InputTokens: 500, OutputTokens: 0}, 400, true, false},
		{"completion-only usage input backfilled", &TokenUsage{InputTokens: 0, OutputTokens: 40}, 400, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOutput := tc.usage != nil && tc.usage.OutputTokens == 0 && tc.outputChars > 0
			if gotOutput != tc.wantOutput {
				t.Errorf("output backfill = %v, want %v", gotOutput, tc.wantOutput)
			}
			gotInput := tc.usage != nil && tc.usage.InputTokens == 0
			if gotInput != tc.wantInput {
				t.Errorf("input backfill = %v, want %v", gotInput, tc.wantInput)
			}
		})
	}
}
