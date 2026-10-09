package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCacheEffMonitor_NoGuidanceWithInsufficientSamples(t *testing.T) {
	m := newCacheEffMonitor()
	// Fewer than cacheEffMinCalls (4) samples
	for i := 0; i < 3; i++ {
		g := m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
		if g != "" {
			t.Fatalf("expected no guidance with < %d samples, got: %s", cacheEffMinCalls, g)
		}
	}
}

func TestCacheEffMonitor_NoGuidanceWithoutCacheActivity(t *testing.T) {
	m := newCacheEffMonitor()
	// Provider with no cache support (all CacheRead=0)
	for i := 0; i < cacheEffWindow; i++ {
		g := m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
		})
		if g != "" {
			t.Fatalf("expected no guidance for non-caching provider, got: %s", g)
		}
	}
}

func TestCacheEffMonitor_NoGuidanceWhenStableHighCacheHit(t *testing.T) {
	m := newCacheEffMonitor()
	// Consistently high cache hit ratio - no storm
	for i := 0; i < cacheEffWindow; i++ {
		g := m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000, // 90% hit rate
		})
		if g != "" {
			t.Fatalf("expected no guidance for stable high cache hit, got: %s", g)
		}
	}
}

func TestCacheEffMonitor_DetectsCacheBustStorm(t *testing.T) {
	m := newCacheEffMonitor()

	// Phase 1: warm cache (high hit ratio)
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000, // 90% hit
		})
	}

	// Phase 2: cache busts (cache_read drops to near zero)
	var guidance string
	for i := 0; i < cacheStormConsecutive+1; i++ {
		guidance = m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0, // 0% hit - cache busted
		})
		if guidance != "" {
			break
		}
	}

	if guidance == "" {
		t.Fatal("expected cache bust storm guidance after consecutive cold calls")
	}

	if !strings.Contains(guidance, "Cache Efficiency Alert") {
		t.Errorf("guidance should mention 'Cache Efficiency Alert', got: %s", guidance)
	}
	if !strings.Contains(guidance, "System prompt instability") {
		t.Errorf("guidance should mention System prompt instability, got: %s", guidance)
	}
}

func TestCacheEffMonitor_FiresOnlyOncePerRun(t *testing.T) {
	m := newCacheEffMonitor()

	// Warm phase
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
	}

	// Trigger storm
	var first string
	for i := 0; i < cacheStormConsecutive+2; i++ {
		first = m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
		})
		if first != "" {
			break
		}
	}

	if first == "" {
		t.Fatal("expected first guidance to fire")
	}

	// Continue with more cold calls - should NOT fire again
	for i := 0; i < 5; i++ {
		g := m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
		})
		if g != "" {
			t.Fatal("expected no second guidance (once-per-run)")
		}
	}
}

func TestCacheEffMonitor_ResetClearsState(t *testing.T) {
	m := newCacheEffMonitor()

	// Build up state
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
	}
	// Trigger storm
	for i := 0; i < cacheStormConsecutive+1; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
		})
	}

	if !m.alerted {
		t.Fatal("expected alerted=true before reset")
	}

	m.reset()

	if m.alerted {
		t.Fatal("expected alerted=false after reset")
	}
	if len(m.samples) != 0 {
		t.Fatal("expected samples cleared after reset")
	}
	if m.warmSeen {
		t.Fatal("expected warmSeen=false after reset")
	}
}

func TestCacheEffMonitor_HitRatio(t *testing.T) {
	m := newCacheEffMonitor()

	// 90% cache hit
	ratio := m.hitRatio(cacheEffSample{input: 1000, cacheRead: 9000, total: 10000})
	if ratio < 0.89 || ratio > 0.91 {
		t.Errorf("expected ~0.90 hit ratio, got %.2f", ratio)
	}

	// Zero total
	ratio = m.hitRatio(cacheEffSample{input: 0, cacheRead: 0, total: 0})
	if ratio != 0 {
		t.Errorf("expected 0 hit ratio for zero total, got %.2f", ratio)
	}
}

func TestCacheEffMonitor_WindowSummary(t *testing.T) {
	m := newCacheEffMonitor()
	m.record(provider.TokenUsage{InputTokens: 1000, CacheRead: 9000})
	m.record(provider.TokenUsage{InputTokens: 5000, CacheRead: 5000})

	summary := m.windowSummary()
	if !strings.Contains(summary, "in=1000") {
		t.Errorf("window summary should contain first sample: %s", summary)
	}
	if !strings.Contains(summary, "in=5000") {
		t.Errorf("window summary should contain second sample: %s", summary)
	}
}

// TestCacheEfficiencyMonitorOpenAICompatSubset pins #1441-B: OpenAI-compat
// providers report CacheRead as a SUBSET of InputTokens (PromptTokens
// already includes cached); the raw sum double-counted it and made the
// warm tier mathematically unreachable (a real 90% hit computed 0.474).
// With normalization the same sample reads 9000/10000 = 0.9.
func TestCacheEfficiencyMonitorOpenAICompatSubset(t *testing.T) {
	m := newCacheEffMonitor()
	// Real-world shape: PromptTokensTotal=10000 includes 9000 cached.
	// DisplayInputTokens normalizes input to 1000 (uncached share).
	g := m.record(provider.TokenUsage{
		InputTokens:       10000,
		CacheRead:         9000,
		PromptTokensTotal: 10000,
	})
	if g != "" {
		t.Fatalf("single sample should not warn yet: %q", g)
	}
	// The stored sample's warm ratio must be 0.9, not 9000/19000=0.474:
	// drive to a verdict via the storm path (warm then cold burst) and
	// confirm warm was RECORDED - with the old double-count, warmSeen
	// could never set and the storm verdict was unreachable.
	m2 := newCacheEffMonitor()
	for i := 0; i < cacheEffMinCalls; i++ {
		m2.record(provider.TokenUsage{InputTokens: 10000, CacheRead: 9000, PromptTokensTotal: 10000})
	}
	if !m2.warmSeen {
		t.Fatal("90% cache-hit samples never recorded warm - subset double-count regression")
	}
}

// recentHitRatio (r20 cache-aware compaction deferral) unit coverage.
func TestCacheEffMonitor_RecentHitRatio(t *testing.T) {
	t.Run("insufficient samples returns 0", func(t *testing.T) {
		m := newCacheEffMonitor()
		for i := 0; i < cacheEffMinCalls-1; i++ {
			m.record(provider.TokenUsage{InputTokens: 100, CacheRead: 90})
		}
		if r := m.recentHitRatio(); r != 0 {
			t.Fatalf("want 0 below cacheEffMinCalls, got %v", r)
		}
	})

	t.Run("empty monitor returns 0", func(t *testing.T) {
		m := newCacheEffMonitor()
		if r := m.recentHitRatio(); r != 0 {
			t.Fatalf("want 0 on empty window, got %v", r)
		}
	})

	t.Run("mean over window", func(t *testing.T) {
		m := newCacheEffMonitor()
		// 3 calls at ratio 0.9 (90/100), 1 call at ratio 0.1 (10/100) -> mean 0.7
		for i := 0; i < 3; i++ {
			m.record(provider.TokenUsage{InputTokens: 10, CacheRead: 90})
		}
		m.record(provider.TokenUsage{InputTokens: 90, CacheRead: 10})
		got := m.recentHitRatio()
		if got < 0.69 || got > 0.71 {
			t.Fatalf("want ~0.7 mean, got %v", got)
		}
	})

	t.Run("no-cache provider stays ~0 (no-op invariant)", func(t *testing.T) {
		m := newCacheEffMonitor()
		for i := 0; i < cacheEffWindow+2; i++ {
			m.record(provider.TokenUsage{InputTokens: 1000, CacheRead: 0})
		}
		if r := m.recentHitRatio(); r != 0 {
			t.Fatalf("want 0 for CacheRead=0 providers, got %v", r)
		}
	})
}
