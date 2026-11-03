package session

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue3086_UsageRebuildVsConcurrentAppend(t *testing.T) {
	s := &Session{Vendor: "v", Endpoint: "e"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.AddUsageHistoryEntry(UsageEntry{
				Vendor:   "v",
				Endpoint: "e",
				Usage:    provider.TokenUsage{InputTokens: 1},
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = s.UsageForEndpoint("v", "e")
		}
	}()
	wg.Wait()

	s.RebuildEndpointStats()
	if got := s.UsageForEndpoint("v", "e"); got.InputTokens != 200 {
		t.Fatalf("aggregated usage = %d, want 200", got.InputTokens)
	}
}

func TestIssue3086_MetricsReadVsConcurrentAppend(t *testing.T) {
	s := &Session{Vendor: "v", Endpoint: "e"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.AppendMetricEvent(metrics.MetricEvent{Vendor: "v", Endpoint: "e", TurnIndex: i})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = s.MetricsForEndpoint("v", "e")
		}
	}()
	wg.Wait()

	s.RebuildEndpointStats()
	if got := len(s.MetricsForEndpoint("v", "e")); got != 200 {
		t.Fatalf("metrics count = %d, want 200", got)
	}
}

func TestIssue3086_RebuildVsConcurrentAppend(t *testing.T) {
	s := &Session{Vendor: "v", Endpoint: "e"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.AddUsageHistoryEntry(UsageEntry{Vendor: "v", Endpoint: "e", Usage: provider.TokenUsage{OutputTokens: 1}})
			s.AppendMetricEvent(metrics.MetricEvent{Vendor: "v", Endpoint: "e"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.RebuildEndpointStats()
		}
	}()
	wg.Wait()
	s.RebuildEndpointStats()
	if got := s.UsageForEndpoint("v", "e"); got.OutputTokens != 100 {
		t.Fatalf("aggregated output = %d, want 100", got.OutputTokens)
	}
}

func TestIssue3086_GuardedMethodsSemantics(t *testing.T) {
	s := &Session{}
	s.AddUsageHistoryEntry(UsageEntry{Vendor: "v", Endpoint: "e", Usage: provider.TokenUsage{InputTokens: 3}})
	s.AppendMetricEvent(metrics.MetricEvent{Vendor: "v", Endpoint: "e"})
	if len(s.UsageHistory) != 1 || len(s.Metrics) != 1 {
		t.Fatalf("guarded append counts: usage=%d metrics=%d, want 1/1", len(s.UsageHistory), len(s.Metrics))
	}
	if got := s.UsageForEndpoint("v", "e"); got.InputTokens != 3 {
		t.Fatalf("lazy rebuild after guarded append: %v", got)
	}

	var nilS *Session
	nilS.AddUsageHistoryEntry(UsageEntry{})
	nilS.AppendMetricEvent(metrics.MetricEvent{})
}
