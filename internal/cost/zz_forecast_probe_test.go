package cost

import (
	"fmt"
	"strings"
	"testing"
)

// r434 pre-execution token forecasting probes. Discriminating power: the
// ForecastTokens function and its band/degradation semantics do not exist
// before this change (compile-level red).

// samplePrompt builds prompts sharing task vocabulary so similarity ranks
// them near the query.
func samplePrompt(i int) string {
	return fmt.Sprintf("fix the parser bug in module %d and add regression tests", i)
}

func TestForecastSimilarRunsGiveTightBand(t *testing.T) {
	var samples []RunSample
	for i := 0; i < 12; i++ {
		samples = append(samples, RunSample{
			FirstPrompt: samplePrompt(i),
			Tokens:      100_000 + i*1_000, // 100k..111k, tight cluster
		})
	}
	fc := ForecastTokens(samples, "fix the parser bug in the token scanner and add regression tests")
	if fc.Neighbors != forecastK {
		t.Fatalf("Neighbors=%d, want K=%d (12 samples capped at K)", fc.Neighbors, forecastK)
	}
	if fc.Degraded {
		t.Fatalf("Degraded=true with 12 usable samples, want false")
	}
	// Band must be drawn from the sample cluster.
	if fc.Low < 100_000 || fc.High > 111_000 {
		t.Fatalf("band [%d,%d] outside sample cluster [100000,111000]", fc.Low, fc.High)
	}
	if fc.Median < fc.Low || fc.Median > fc.High {
		t.Fatalf("median %d outside band [%d,%d]", fc.Median, fc.Low, fc.High)
	}
}

func TestForecastThinSampleDegrades(t *testing.T) {
	samples := []RunSample{
		{FirstPrompt: "refactor auth middleware", Tokens: 50_000},
		{FirstPrompt: "write release notes for v2", Tokens: 8_000},
		{FirstPrompt: "investigate flaky e2e test", Tokens: 30_000},
	}
	fc := ForecastTokens(samples, "fix the parser bug")
	if !fc.Degraded {
		t.Fatalf("Degraded=false with 3 usable samples (< minNeighbors=%d), want true", minNeighbors)
	}
	if fc.Neighbors != 3 {
		t.Fatalf("Neighbors=%d, want 3 (all samples used as the fallback pool)", fc.Neighbors)
	}
}

func TestForecastNoUsableSamples(t *testing.T) {
	// Zero-token runs and an empty pool must yield an empty forecast, not
	// a panic or a fabricated band.
	fc := ForecastTokens([]RunSample{{FirstPrompt: "x", Tokens: 0}}, "anything")
	if fc.Neighbors != 0 || fc.Low != 0 || fc.High != 0 || fc.Median != 0 {
		t.Fatalf("zero-token-only pool: got %+v, want zero forecast", fc)
	}
	fc = ForecastTokens(nil, "anything")
	if fc.Neighbors != 0 {
		t.Fatalf("empty pool: got %+v, want zero forecast", fc)
	}
}

func TestForecastSingleSampleBandCollapses(t *testing.T) {
	fc := ForecastTokens([]RunSample{{FirstPrompt: "same task", Tokens: 42_000}}, "same task")
	if fc.Neighbors != 1 || fc.Low != 42_000 || fc.Median != 42_000 || fc.High != 42_000 {
		t.Fatalf("single sample: got %+v, want collapsed band at 42000", fc)
	}
}

func TestForecastDissimilarRankedBehindSimilar(t *testing.T) {
	// Two clusters: parser-fix runs (~100k tokens) and doc-writing runs
	// (~5k tokens). A parser-flavored query must draw its band from the
	// parser cluster even though doc runs are in the pool.
	var samples []RunSample
	for i := 0; i < 8; i++ {
		samples = append(samples, RunSample{
			FirstPrompt: samplePrompt(i),
			Tokens:      100_000,
		})
	}
	for i := 0; i < 8; i++ {
		samples = append(samples, RunSample{
			FirstPrompt: strings.Repeat("write documentation chapters about cooking recipes ", 4),
			Tokens:      5_000,
		})
	}
	fc := ForecastTokens(samples, "fix the parser bug and add regression tests")
	if fc.High != 100_000 || fc.Low != 100_000 {
		t.Fatalf("band [%d,%d] not drawn from parser cluster (all 100k), doc cluster (5k) leaked in", fc.Low, fc.High)
	}
}
