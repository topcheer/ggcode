package agent

import (
	"context"
	"testing"
)

func TestNewEmergentDriftMonitor(t *testing.T) {
	mon := newEmergentDriftMonitor()
	if mon == nil {
		t.Fatal("newEmergentDriftMonitor returned nil")
	}
	if mon.driftThreshold != defaultDriftThreshold {
		t.Errorf("expected driftThreshold %v, got %v", defaultDriftThreshold, mon.driftThreshold)
	}
}

func TestRecordInteraction(t *testing.T) {
	mon := newEmergentDriftMonitor()

	// Record some interactions
	mon.recordInteraction("agent-1", "agent-2", "message", true)
	mon.recordInteraction("agent-2", "agent-1", "message", true)
	mon.recordInteraction("agent-1", "agent-3", "task_delegation", false)

	if len(mon.interactionHistory) != 3 {
		t.Errorf("expected 3 interactions, got %d", len(mon.interactionHistory))
	}

	// Check pattern normalization (direction-agnostic for messages)
	messagePattern := "agent-1<->agent-2:message"
	if count, ok := mon.patternCounts[messagePattern]; !ok || count != 2 {
		t.Errorf("expected pattern %s to have count 2, got %d (exists: %v)", messagePattern, count, ok)
	}
}

func TestCheckDrift_InsufficientData(t *testing.T) {
	mon := newEmergentDriftMonitor()

	// Not enough data yet
	detected, reason := mon.checkDrift(context.Background())
	if detected {
		t.Errorf("expected no drift with insufficient data, got: %s", reason)
	}
}

func TestCheckDrift_BaselineCalculation(t *testing.T) {
	mon := newEmergentDriftMonitor()

	// Add enough interactions to trigger baseline calculation
	for i := 0; i < baselineRecalcInterval; i++ {
		mon.recordInteraction("agent-1", "agent-2", "message", true)
	}

	if mon.lastBaselineSnapshot == nil {
		t.Fatal("baseline should be calculated after baselineRecalcInterval interactions")
	}

	// Check drift detection now works
	detected, reason := mon.checkDrift(context.Background())
	if detected {
		t.Errorf("expected no drift with uniform patterns, got: %s", reason)
	}
}

func TestCheckDrift_PatternDrift(t *testing.T) {
	mon := newEmergentDriftMonitor()

	// Establish baseline with one pattern
	for i := 0; i < 100; i++ {
		mon.recordInteraction("agent-1", "agent-2", "message", true)
	}
	// Manually capture baseline
	mon.recalculateBaseline()

	// Introduce drift with different pattern - add 299 (not a multiple of 200)
	for i := 0; i < 299; i++ {
		mon.recordInteraction("agent-3", "agent-4", "message", true)
	}

	detected, reason := mon.checkDrift(context.Background())
	if !detected {
		t.Errorf("expected drift detection with changed patterns, got: detected=%v, reason=%q", detected, reason)
	}
	if reason == "" {
		t.Errorf("expected drift reason, got empty string")
	}
}

func TestHistoryPruning(t *testing.T) {
	mon := newEmergentDriftMonitor()

	// Add more than maxHistory interactions
	for i := 0; i < mon.maxHistory+100; i++ {
		mon.recordInteraction("agent-1", "agent-2", "message", true)
	}

	if len(mon.interactionHistory) > mon.maxHistory {
		t.Errorf("expected history to be pruned to maxHistory=%d, got %d",
			mon.maxHistory, len(mon.interactionHistory))
	}
}

func TestIntMin(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{0, 0, 0},
		{-1, 1, -1},
	}

	for _, tt := range tests {
		got := intMin(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("intMin(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAbsFloat64(t *testing.T) {
	tests := []struct {
		x, want float64
	}{
		{1.5, 1.5},
		{-1.5, 1.5},
		{0, 0},
		{-0.0, 0},
	}

	for _, tt := range tests {
		got := absFloat64(tt.x)
		if got != tt.want {
			t.Errorf("absFloat64(%v) = %v, want %v", tt.x, got, tt.want)
		}
	}
}

func TestGlobalFunctions(t *testing.T) {
	// Test exported functions don't panic
	RecordAgentInteraction("agent-1", "agent-2", "message", true)
	detected, reason := CheckEmergentDrift(context.Background())
	// Insufficient data means no drift expected
	if detected {
		t.Logf("Drift detected (unexpected): %s", reason)
	}
}

func BenchmarkRecordInteraction(b *testing.B) {
	mon := newEmergentDriftMonitor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mon.recordInteraction("agent-1", "agent-2", "message", true)
	}
}

func BenchmarkCheckDrift(b *testing.B) {
	mon := newEmergentDriftMonitor()
	// Populate with data
	for i := 0; i < baselineRecalcInterval*2; i++ {
		mon.recordInteraction("agent-1", "agent-2", "message", true)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mon.checkDrift(context.Background())
	}
}
