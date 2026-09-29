package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestUsageBySource_NilSession(t *testing.T) {
	if got := UsageBySource(nil); got != nil {
		t.Fatalf("UsageBySource(nil) = %v, want nil", got)
	}
}

func TestUsageBySource_AggregatesAndSorts(t *testing.T) {
	ses := &Session{UsageHistory: []UsageEntry{
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 100, OutputTokens: 50}},
		{Source: "subagent", Usage: provider.TokenUsage{InputTokens: 400, OutputTokens: 100}},
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 30, OutputTokens: 20}},
		{Source: "compaction", Usage: provider.TokenUsage{InputTokens: 200}},
		// zero-total entry must be omitted
		{Source: "verify", Usage: provider.TokenUsage{}},
	}}
	got := UsageBySource(ses)
	if len(got) != 3 {
		t.Fatalf("got %d sources, want 3: %+v", len(got), got)
	}
	if got[0].Source != "subagent" || got[0].Usage.Total() != 500 {
		t.Errorf("first = %+v, want subagent 500", got[0])
	}
	if got[1].Source != "agent" || got[1].Usage.Total() != 200 {
		t.Errorf("second = %+v, want agent 200 (tie with compaction, name tie-break)", got[1])
	}
	if got[2].Source != "compaction" || got[2].Usage.Total() != 200 {
		t.Errorf("third = %+v, want compaction 200", got[2])
	}
}

func TestUsageBySource_EmptySourceGroupedAsUnattributed(t *testing.T) {
	ses := &Session{UsageHistory: []UsageEntry{
		{Usage: provider.TokenUsage{OutputTokens: 10}}, // legacy, no Source
		{Source: "agent", Usage: provider.TokenUsage{OutputTokens: 5}},
	}}
	got := UsageBySource(ses)
	if len(got) != 2 {
		t.Fatalf("got %d sources, want 2: %+v", len(got), got)
	}
	if got[0].Source != "unattributed" {
		t.Errorf("first source = %q, want unattributed", got[0].Source)
	}
}

func TestUsageBySource_TieBreakByName(t *testing.T) {
	ses := &Session{UsageHistory: []UsageEntry{
		{Source: "verify", Usage: provider.TokenUsage{InputTokens: 10}},
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 10}},
	}}
	got := UsageBySource(ses)
	if got[0].Source != "agent" || got[1].Source != "verify" {
		t.Errorf("tie-break order = [%s %s], want [agent verify]", got[0].Source, got[1].Source)
	}
}
