package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCacheEffMonitor_NoGuidanceWithInsufficientSamples(t *testing.T) {
	m := newCacheEffMonitor()

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

	for i := 0; i < cacheEffWindow; i++ {
		g := m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
		if g != "" {
			t.Fatalf("expected no guidance for stable high cache hit, got: %s", g)
		}
	}
}

func TestCacheEffMonitor_DetectsCacheBustStorm(t *testing.T) {
	m := newCacheEffMonitor()

	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
	}

	var guidance string
	for i := 0; i < cacheStormConsecutive+1; i++ {
		guidance = m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
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

	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
	}

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

	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		})
	}

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

	ratio := m.hitRatio(cacheEffSample{input: 1000, cacheRead: 9000, total: 10000})
	if ratio < 0.89 || ratio > 0.91 {
		t.Errorf("expected ~0.90 hit ratio, got %.2f", ratio)
	}

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

func TestCacheEfficiencyMonitorOpenAICompatSubset(t *testing.T) {
	m := newCacheEffMonitor()

	g := m.record(provider.TokenUsage{
		InputTokens:       10000,
		CacheRead:         9000,
		PromptTokensTotal: 10000,
	})
	if g != "" {
		t.Fatalf("single sample should not warn yet: %q", g)
	}

	m2 := newCacheEffMonitor()
	for i := 0; i < cacheEffMinCalls; i++ {
		m2.record(provider.TokenUsage{InputTokens: 10000, CacheRead: 9000, PromptTokensTotal: 10000})
	}
	if !m2.warmSeen {
		t.Fatal("90% cache-hit samples never recorded warm - subset double-count regression")
	}
}
