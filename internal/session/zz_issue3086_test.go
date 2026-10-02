package session

// Regression probes for #3086 (endpoint_stats data race): the
// endpointStatsMu mutex covered only the aggregate maps; the SOURCE slices
// (UsageHistory/Metrics) were appended by cross-goroutine callbacks
// (TUI/IM/desktop) as bare `s.X = append(s.X, e)` while
// UsageForEndpoint/MetricsForEndpoint/rebuild ranged them lock-free.
//
// These probes run under `go test -race`: before the fix the concurrent
// append+read trips the race detector; after it, all access passes
// through endpointStatsMu-guarded methods.

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue3086_UsageRebuildVsConcurrentAppend: one goroutine appends via
// AddUsageHistoryEntry while the reader path triggers the lazy rebuild.
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
	// The lazy rebuild fires at most once (while the aggregate map is
	// empty), so a mid-race reader freezes a partial aggregate - that is
	// the documented lazy-migration semantics, not a race. Rebuild once
	// after the writers settle to assert every guarded append landed.
	s.RebuildEndpointStats()
	if got := s.UsageForEndpoint("v", "e"); got.InputTokens != 200 {
		t.Fatalf("aggregated usage = %d, want 200", got.InputTokens)
	}
}

// TestIssue3086_MetricsReadVsConcurrentAppend: same shape for Metrics.
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
	// Same lazy-migration semantics as the usage probe: rebuild after the
	// writers settle.
	s.RebuildEndpointStats()
	if got := len(s.MetricsForEndpoint("v", "e")); got != 200 {
		t.Fatalf("metrics count = %d, want 200", got)
	}
}

// TestIssue3086_RebuildVsConcurrentAppend: the explicit rebuild path races
// too (it ranges both source slices).
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

// TestIssue3086_GuardedMethodsSemantics: the new appends behave like plain
// slice appends and the legacy zero-value paths still hold.
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
	// Nil-safe, matching package conventions.
	var nilS *Session
	nilS.AddUsageHistoryEntry(UsageEntry{})
	nilS.AppendMetricEvent(metrics.MetricEvent{})
}
