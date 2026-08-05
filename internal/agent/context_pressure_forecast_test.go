package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestPressureForecaster_NoGuidanceWithoutContextWindow(t *testing.T) {
	f := newPressureForecaster()
	// contextWindow is 0 (unknown) - no guidance possible
	for i := 0; i < 10; i++ {
		g := f.record(i, provider.TokenUsage{
			InputTokens: 1000 + i*500,
			CacheRead:   5000,
		})
		if g != "" {
			t.Fatalf("expected no guidance without context window, got: %s", g)
		}
	}
}

func TestPressureForecaster_NoGuidanceWithInsufficientSamples(t *testing.T) {
	f := newPressureForecaster()
	f.setContextWindow(200000)
	// Only 2 samples (< pressureMinSamples=3)
	for i := 0; i < 2; i++ {
		g := f.record(i, provider.TokenUsage{
			InputTokens: 1000 + i*1000,
			CacheRead:   10000,
		})
		if g != "" {
			t.Fatalf("expected no guidance with < %d samples, got: %s", pressureMinSamples, g)
		}
	}
}

func TestPressureForecaster_NoGuidanceWhenPlentyOfRoom(t *testing.T) {
	f := newPressureForecaster()
	f.setContextWindow(200000) // 200K window

	// Growing slowly, lots of room
	for i := 0; i < 8; i++ {
		g := f.record(i, provider.TokenUsage{
			InputTokens: 1000 + i*500, // slow growth
			CacheRead:   10000,
		})
		if g != "" {
			t.Fatalf("expected no guidance with plenty of room, got at iter %d: %s", i, g)
		}
	}
}

func TestPressureForecaster_WarnsWhenApproachingThreshold(t *testing.T) {
	f := newPressureForecaster()
	f.setContextWindow(100000) // 100K window, compaction at 80K

	// Simulate fast growth: starting at 60K, growing 5K per iteration
	// After a few iterations, we'll be within ~4 iterations of 80K threshold
	total := 60000
	var guidance string
	for i := 0; i < 8; i++ {
		guidance = f.record(i, provider.TokenUsage{
			InputTokens: total,
			CacheRead:   0, // no cache for simplicity
		})
		if guidance != "" {
			break
		}
		total += 5000 // 5K growth per iteration
	}

	if guidance == "" {
		t.Fatal("expected pressure warning as context approaches threshold")
	}

	if !strings.Contains(guidance, "Context Pressure") {
		t.Errorf("guidance should mention 'Context Pressure', got: %s", guidance)
	}
}

func TestPressureForecaster_NoGuidanceWhenNotGrowing(t *testing.T) {
	f := newPressureForecaster()
	f.setContextWindow(100000)

	// Static context (no growth) - should never warn
	for i := 0; i < 10; i++ {
		g := f.record(i, provider.TokenUsage{
			InputTokens: 60000, // constant
			CacheRead:   0,
		})
		if g != "" {
			t.Fatalf("expected no guidance when context is not growing, got: %s", g)
		}
	}
}

func TestPressureForecaster_EstimateGrowthRate(t *testing.T) {
	f := newPressureForecaster()

	// Linear growth: 10K, 15K, 20K, 25K at iterations 0,1,2,3
	f.samples = []pressureSample{
		{iteration: 0, totalTokens: 10000},
		{iteration: 1, totalTokens: 15000},
		{iteration: 2, totalTokens: 20000},
		{iteration: 3, totalTokens: 25000},
	}

	rate := f.estimateGrowthRate()
	// Should be ~5000 tokens per iteration
	if rate < 4900 || rate > 5100 {
		t.Errorf("expected growth rate ~5000, got %.1f", rate)
	}
}

func TestPressureForecaster_EstimateGrowthRateWithTwoSamples(t *testing.T) {
	f := newPressureForecaster()
	f.samples = []pressureSample{
		{iteration: 0, totalTokens: 10000},
		{iteration: 5, totalTokens: 60000},
	}

	rate := f.estimateGrowthRate()
	// (60000-10000)/(5-0) = 10000 per iteration
	if rate < 9900 || rate > 10100 {
		t.Errorf("expected growth rate ~10000, got %.1f", rate)
	}
}

func TestPressureForecaster_ResetClearsState(t *testing.T) {
	f := newPressureForecaster()
	f.setContextWindow(100000)

	// Build up samples
	for i := 0; i < 5; i++ {
		f.record(i, provider.TokenUsage{
			InputTokens: 60000 + i*5000,
			CacheRead:   0,
		})
	}

	if len(f.samples) == 0 {
		t.Fatal("expected samples after recording")
	}

	f.reset()

	if len(f.samples) != 0 {
		t.Fatal("expected samples cleared after reset")
	}
	if f.warningCount != 0 {
		t.Fatal("expected warningCount=0 after reset")
	}
}

func TestPressureForecaster_MaxWarningsCap(t *testing.T) {
	f := newPressureForecaster()
	f.setContextWindow(100000)

	// Fast approach to threshold
	total := 65000
	var warningCount int
	for i := 0; i < 30; i++ {
		g := f.record(i, provider.TokenUsage{
			InputTokens: total,
			CacheRead:   0,
		})
		if g != "" {
			warningCount++
		}
		if total < 78000 {
			total += 3000
		}
	}

	if warningCount > pressureMaxWarnings {
		t.Errorf("expected at most %d warnings, got %d", pressureMaxWarnings, warningCount)
	}
}
