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
