package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestAppendConsumerAttribution_SingleConsumerSilent(t *testing.T) {
	var sb strings.Builder
	history := []session.UsageEntry{
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 100}},
	}
	appendConsumerAttribution(&sb, history, 100)
	if sb.Len() != 0 {
		t.Errorf("single consumer must render nothing, got %q", sb.String())
	}
}

func TestAppendConsumerAttribution_MultiConsumerShares(t *testing.T) {
	var sb strings.Builder
	history := []session.UsageEntry{
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 100}},
		{Source: "subagent", Usage: provider.TokenUsage{InputTokens: 300}},
	}
	appendConsumerAttribution(&sb, history, 400)
	out := sb.String()
	if !strings.Contains(out, "By consumer:") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "subagent") || !strings.Contains(out, "agent") {
		t.Errorf("missing consumer rows: %q", out)
	}
	if !strings.Contains(out, "75%") || !strings.Contains(out, "25%") {
		t.Errorf("missing share percentages: %q", out)
	}
	// subagent (300 tok) outranks agent (100 tok): its row must come first.
	if strings.Index(out, "subagent") > strings.Index(out, "agent") {
		t.Errorf("rows not sorted desc by tokens: %q", out)
	}
}

func TestAppendConsumerAttribution_RecomputesDenominator(t *testing.T) {
	var sb strings.Builder
	history := []session.UsageEntry{
		{Source: "compaction", Usage: provider.TokenUsage{InputTokens: 50}},
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 50}},
	}
	// sessionTotal stale/zero: denominator must come from the entries.
	appendConsumerAttribution(&sb, history, 0)
	out := sb.String()
	if !strings.Contains(out, "50%") {
		t.Errorf("expected 50%% shares, got %q", out)
	}
	if strings.Contains(out, "NaN") {
		t.Errorf("NaN share rendered: %q", out)
	}
}
