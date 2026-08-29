package provider

// Regression tests for GitHub issue #1286: OpenAI streaming usage chunks
// with all-zero fields (sent by one-api/new-api relay compat layers) must
// not clear previously accumulated usage nor suppress the estimation
// fallback. Mirrors the anthropic.go #722/#1168 zero guards.

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestIssue1286_AllZeroUsageKeepsAccumulated(t *testing.T) {
	cur := &TokenUsage{InputTokens: 100, OutputTokens: 50, PromptTokensTotal: 100, CacheRead: 7}
	got := applyOpenAIUsage(cur, openai.Usage{}) // all-zero chunk from a relay
	if got != cur {
		t.Fatal("all-zero usage must return the accumulated usage unchanged")
	}
	if got.InputTokens != 100 || got.OutputTokens != 50 || got.CacheRead != 7 {
		t.Fatalf("accumulated fields clobbered: %+v", got)
	}
}

func TestIssue1286_AllZeroOnNilStaysNil(t *testing.T) {
	// If the ONLY usage the stream ever produced was all-zero, usage must
	// stay nil so the CountTokens estimation fallback still runs.
	if got := applyOpenAIUsage(nil, openai.Usage{PromptTokens: 0, CompletionTokens: 0}); got != nil {
		t.Fatalf("all-zero-only stream must keep usage nil, got %+v", got)
	}
}

func TestIssue1286_ZeroFieldsDoNotClobberPartialValues(t *testing.T) {
	// First chunk carries prompt tokens only (common: relay sends input
	// usage early), second carries completion only, third repeats with
	// zeros in the prompt slot - nothing may be lost.
	cur := applyOpenAIUsage(nil, openai.Usage{PromptTokens: 120})
	if cur == nil || cur.InputTokens != 120 || cur.OutputTokens != 0 {
		t.Fatalf("first chunk: %+v", cur)
	}
	cur = applyOpenAIUsage(cur, openai.Usage{CompletionTokens: 80})
	if cur.InputTokens != 120 || cur.OutputTokens != 80 {
		t.Fatalf("field-wise merge failed: %+v", cur)
	}
	// Late chunk with zero prompt_tokens must not zero the 120.
	cur = applyOpenAIUsage(cur, openai.Usage{CompletionTokens: 90})
	if cur.InputTokens != 120 || cur.OutputTokens != 90 {
		t.Fatalf("zero field clobbered accumulated value: %+v", cur)
	}
}

func TestIssue1286_RealUsageStillOverwrites(t *testing.T) {
	cur := &TokenUsage{InputTokens: 1, OutputTokens: 1, PromptTokensTotal: 1}
	got := applyOpenAIUsage(cur, openai.Usage{PromptTokens: 200, CompletionTokens: 100})
	if got.InputTokens != 200 || got.OutputTokens != 100 || got.PromptTokensTotal != 200 {
		t.Fatalf("non-zero usage must update accumulated values: %+v", got)
	}
}
