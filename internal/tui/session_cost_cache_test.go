package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestSessionCostSnapshotCache(t *testing.T) {
	m := newTestModel()
	m.session = &session.Session{
		ID:       "s1",
		Vendor:   "zai",
		Endpoint: "cn-coding-anthropic",
		Model:    "glm-5.2",
		TokenUsage: provider.TokenUsage{
			InputTokens:  100,
			OutputTokens: 10,
		},
	}
	m.session.UsageHistory = []session.UsageEntry{
		{Vendor: "zai", Endpoint: "cn-coding-anthropic", Model: "glm-5.2",
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 1}},
	}

	cost1, usage1 := m.sessionCostSnapshot()
	if usage1.InputTokens != 10 {
		t.Fatalf("usage aggregation: got input=%d want 10", usage1.InputTokens)
	}

	cost2, usage2 := m.sessionCostSnapshot()
	if cost1 != cost2 || usage1 != usage2 {
		t.Fatalf("cache miss on identical input: %v/%v vs %v/%v", cost1, usage1, cost2, usage2)
	}

	m.session.UsageHistory = append(m.session.UsageHistory, session.UsageEntry{
		Vendor: "zai", Endpoint: "cn-coding-anthropic", Model: "glm-5.2",
		Usage: provider.TokenUsage{InputTokens: 5, OutputTokens: 0},
	})
	_, usage3 := m.sessionCostSnapshot()
	if usage3.InputTokens != 15 {
		t.Fatalf("growth invalidation: got input=%d want 15", usage3.InputTokens)
	}

	m.session = &session.Session{
		ID:       "s2",
		Vendor:   "zai",
		Endpoint: "cn-coding-anthropic",
		Model:    "glm-5.2",
	}
	m.session.UsageHistory = []session.UsageEntry{
		{Vendor: "zai", Endpoint: "cn-coding-anthropic", Model: "glm-5.2",
			Usage: provider.TokenUsage{InputTokens: 99, OutputTokens: 9}},
	}
	_, usage4 := m.sessionCostSnapshot()
	if usage4.InputTokens != 99 {
		t.Fatalf("session switch invalidation: got input=%d want 99", usage4.InputTokens)
	}
}

func TestEstimateSessionCostConsistency(t *testing.T) {
	m := newTestModel()
	m.session = &session.Session{ID: "s1"}
	m.session.UsageHistory = []session.UsageEntry{
		{Vendor: "unknown-metered", Endpoint: "e", Model: "m",
			Usage: provider.TokenUsage{InputTokens: 1_000_000}},
	}

	if c := m.estimateSessionCost(); c != 0 {
		t.Fatalf("unknown vendor should cost 0, got %v", c)
	}
}
