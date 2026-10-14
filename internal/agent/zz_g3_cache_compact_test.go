package agent

// Research G3 (context compaction benchmark verdict): cache-aware
// auto-compact trigger. The compaction decision consumes the rolling prompt
// cache hit ratio: a warm cache (>= cacheHitRatioThreshold) inside the
// headroom band defers compaction; past the hard ceiling it never defers.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// testUsage builds a TokenUsage with input=non-cached input tokens,
// cacheRead=cache-read tokens, per the #1441-B DisplayInputTokens semantics.
func testUsage(input, cacheRead, _ int) provider.TokenUsage {
	return provider.TokenUsage{InputTokens: input, CacheRead: cacheRead}
}

func TestG3DeferCompactWhileCacheWarm(t *testing.T) {
	thr := 100_000
	ceiling := int(float64(thr) * cacheAwareDeferHeadroom) // 115000 for thr=100k
	cases := []struct {
		name    string
		tokens  int
		ratio   float64
		wantDef bool
	}{
		{"warm cache in band", thr + 1000, 0.80, true},
		{"warm cache at last in-band token", ceiling, 0.90, true},
		{"warm cache past ceiling", ceiling + 1, 0.95, false},
		{"cold cache in band", thr + 1000, 0.30, false},
		{"ratio exactly at threshold", thr + 1000, 0.50, true},
	}
	for _, c := range cases {
		if got := deferCompactWhileCacheWarm(c.tokens, thr, c.ratio); got != c.wantDef {
			t.Errorf("%s: defer=%v want %v (tokens=%d ratio=%.2f)", c.name, got, c.wantDef, c.tokens, c.ratio)
		}
	}
}

func TestG3RollingHitRatio(t *testing.T) {
	m := newCacheEffMonitor()
	// Below min samples: not ok.
	if _, ok := m.rollingHitRatio(); ok {
		t.Fatal("insufficient samples must report ok=false")
	}
	// Warm samples only: high ratio.
	for i := 0; i < cacheEffWindow; i++ {
		m.record(testUsage(1000, 9000, 0))
	}
	if r, ok := m.rollingHitRatio(); !ok || r < cacheHitRatioThreshold {
		t.Fatalf("warm window ratio=%v ok=%v, want ok && >= %.2f", r, ok, cacheHitRatioThreshold)
	}
	// Cold samples only: low ratio -> caller must not defer.
	m2 := newCacheEffMonitor()
	for i := 0; i < cacheEffWindow; i++ {
		m2.record(testUsage(10_000, 0, 0))
	}
	if r, ok := m2.rollingHitRatio(); !ok || r >= cacheHitRatioThreshold {
		t.Fatalf("cold window ratio=%v ok=%v, want ok && < %.2f", r, ok, cacheHitRatioThreshold)
	}
}
