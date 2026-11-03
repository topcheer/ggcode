package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestIssue1286_AllZeroUsageKeepsAccumulated(t *testing.T) {
	cur := &TokenUsage{InputTokens: 100, OutputTokens: 50, PromptTokensTotal: 100, CacheRead: 7}
	got := applyOpenAIUsage(cur, openai.Usage{})
	if got != cur {
		t.Fatal("all-zero usage must return the accumulated usage unchanged")
	}
	if got.InputTokens != 100 || got.OutputTokens != 50 || got.CacheRead != 7 {
		t.Fatalf("accumulated fields clobbered: %+v", got)
	}
}

func TestIssue1286_AllZeroOnNilStaysNil(t *testing.T) {

	if got := applyOpenAIUsage(nil, openai.Usage{PromptTokens: 0, CompletionTokens: 0}); got != nil {
		t.Fatalf("all-zero-only stream must keep usage nil, got %+v", got)
	}
}

func TestIssue1286_ZeroFieldsDoNotClobberPartialValues(t *testing.T) {

	cur := applyOpenAIUsage(nil, openai.Usage{PromptTokens: 120})
	if cur == nil || cur.InputTokens != 120 || cur.OutputTokens != 0 {
		t.Fatalf("first chunk: %+v", cur)
	}
	cur = applyOpenAIUsage(cur, openai.Usage{CompletionTokens: 80})
	if cur.InputTokens != 120 || cur.OutputTokens != 80 {
		t.Fatalf("field-wise merge failed: %+v", cur)
	}

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
