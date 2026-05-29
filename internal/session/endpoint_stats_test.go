package session

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestEndpointStatsKey(t *testing.T) {
	if got := EndpointStatsKey("zai", "default"); got != "zai/default" {
		t.Fatalf("expected zai/default, got %q", got)
	}
	if got := EndpointStatsKey("", "default"); got != "default" {
		t.Fatalf("expected default, got %q", got)
	}
}

func TestUsageForEndpointUsesCompositeBuckets(t *testing.T) {
	ses := NewSession("zai", "default", "glm")
	ses.AddUsageForEndpoint("zai", "default", provider.TokenUsage{InputTokens: 10, OutputTokens: 2})
	ses.AddUsageForEndpoint("zai", "default", provider.TokenUsage{InputTokens: 5, CacheRead: 20})
	ses.AddUsageForEndpoint("openai", "default", provider.TokenUsage{InputTokens: 99})

	got := ses.UsageForEndpoint("zai", "default")
	if got.InputTokens != 15 || got.OutputTokens != 2 || got.CacheRead != 20 {
		t.Fatalf("unexpected usage bucket: %+v", got)
	}
}

func TestMetricsForEndpointUsesCompositeBuckets(t *testing.T) {
	now := time.Now()
	ses := NewSession("zai", "default", "glm")
	ses.AppendMetricForEndpoint("zai", "default", metrics.MetricEvent{Timestamp: now, Type: "llm", Vendor: "zai", Endpoint: "default"})
	ses.AppendMetricForEndpoint("openai", "default", metrics.MetricEvent{Timestamp: now, Type: "tool", Vendor: "openai", Endpoint: "default"})

	got := ses.MetricsForEndpoint("zai", "default")
	if len(got) != 1 || got[0].Vendor != "zai" {
		t.Fatalf("unexpected metrics bucket: %+v", got)
	}
}

func TestRebuildEndpointStatsFromHistory(t *testing.T) {
	now := time.Now()
	ses := &Session{
		Vendor:   "zai",
		Endpoint: "default",
		Model:    "glm",
		UsageHistory: []UsageEntry{
			{Vendor: "zai", Endpoint: "default", Usage: provider.TokenUsage{InputTokens: 10}},
			{Vendor: "openai", Endpoint: "default", Usage: provider.TokenUsage{OutputTokens: 3}},
		},
		Metrics: []metrics.MetricEvent{
			{Timestamp: now, Type: "llm", Vendor: "zai", Endpoint: "default"},
			{Timestamp: now, Type: "tool", Vendor: "openai", Endpoint: "default"},
		},
	}

	ses.RebuildEndpointStats()

	if got := ses.UsageForEndpoint("zai", "default"); got.InputTokens != 10 {
		t.Fatalf("expected rebuilt zai usage, got %+v", got)
	}
	if got := ses.UsageForEndpoint("openai", "default"); got.OutputTokens != 3 {
		t.Fatalf("expected rebuilt openai usage, got %+v", got)
	}
	if got := ses.MetricsForEndpoint("zai", "default"); len(got) != 1 {
		t.Fatalf("expected rebuilt zai metrics, got %d", len(got))
	}
}

func TestUsageForEndpointFallsBackToLegacyTotal(t *testing.T) {
	ses := &Session{
		Vendor:     "zai",
		Endpoint:   "default",
		TokenUsage: provider.TokenUsage{InputTokens: 42},
	}

	if got := ses.UsageForEndpoint("zai", "default"); got.InputTokens != 42 {
		t.Fatalf("expected legacy fallback usage, got %+v", got)
	}
}

func TestUsageForEndpointRebuildsMissingBucketsFromHistory(t *testing.T) {
	ses := &Session{
		Vendor:   "zai",
		Endpoint: "default",
		UsageHistory: []UsageEntry{
			{Vendor: "zai", Endpoint: "default", Usage: provider.TokenUsage{InputTokens: 7}},
			{Vendor: "zai", Endpoint: "default", Usage: provider.TokenUsage{OutputTokens: 2}},
		},
	}

	got := ses.UsageForEndpoint("zai", "default")
	if got.InputTokens != 7 || got.OutputTokens != 2 {
		t.Fatalf("expected rebuilt history usage, got %+v", got)
	}
}

func TestMetricsForEndpointFallsBackToLegacyMetrics(t *testing.T) {
	ses := &Session{
		Vendor:   "zai",
		Endpoint: "default",
		Metrics:  []metrics.MetricEvent{{Type: "llm"}},
	}

	if got := ses.MetricsForEndpoint("zai", "default"); len(got) != 1 {
		t.Fatalf("expected legacy fallback metrics, got %d", len(got))
	}
}

func TestMetricsForEndpointReturnsCopy(t *testing.T) {
	ses := NewSession("zai", "default", "glm")
	ses.AppendMetricForEndpoint("zai", "default", metrics.MetricEvent{Type: "llm"})

	got := ses.MetricsForEndpoint("zai", "default")
	got[0].Type = "tool"

	again := ses.MetricsForEndpoint("zai", "default")
	if len(again) != 1 || again[0].Type != "llm" {
		t.Fatalf("expected copied metrics slice, got %+v", again)
	}
}
