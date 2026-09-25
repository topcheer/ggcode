package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCacheEffMonitor_NoGuidanceWithInsufficientSamples(t *testing.T) {
	m := newCacheEffMonitor()
	// Fewer than cacheEffMinCalls (4) samples
	for i := 0; i < 3; i++ {
		g := m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		}, cacheReqFingerprint{})
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
		}, cacheReqFingerprint{})
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
		}, cacheReqFingerprint{})
		if g != "" {
			t.Fatalf("expected no guidance for stable high cache hit, got: %s", g)
		}
	}
}

func TestCacheEffMonitor_DetectsCacheBustStorm(t *testing.T) {
	m := newCacheEffMonitor()

	// Phase 1: warm cache (high hit ratio). Zero fingerprints keep the
	// attribution disabled (unknown), so the generic cause list is used.
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000, // 90% hit
		}, cacheReqFingerprint{})
	}

	// Phase 2: cache busts (cache_read drops to near zero)
	var guidance string
	for i := 0; i < cacheStormConsecutive+1; i++ {
		guidance = m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0, // 0% hit - cache busted
		}, cacheReqFingerprint{})
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
	if strings.Contains(guidance, "Attributed cause:") {
		t.Errorf("zero fingerprints must stay unattributed, got: %s", guidance)
	}
	if !strings.Contains(guidance, "Likely causes and fixes:") {
		t.Errorf("unattributed storm should keep the generic cause list, got: %s", guidance)
	}
}

func TestCacheEffMonitor_FiresOnlyOncePerRun(t *testing.T) {
	m := newCacheEffMonitor()

	// Warm phase
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 1000,
			CacheRead:   9000,
		}, cacheReqFingerprint{})
	}

	// Trigger storm
	var first string
	for i := 0; i < cacheStormConsecutive+2; i++ {
		first = m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
		}, cacheReqFingerprint{})
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
		}, cacheReqFingerprint{})
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
		}, cacheReqFingerprint{})
	}
	// Trigger storm
	for i := 0; i < cacheStormConsecutive+1; i++ {
		m.record(provider.TokenUsage{
			InputTokens: 10000,
			CacheRead:   0,
		}, cacheReqFingerprint{})
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
	if m.hasLastWarm {
		t.Fatal("expected hasLastWarm=false after reset")
	}
	if !m.lastWarmAt.IsZero() || !m.firstColdAt.IsZero() {
		t.Fatal("expected warm/cold timestamps cleared after reset")
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
	m.record(provider.TokenUsage{InputTokens: 1000, CacheRead: 9000}, cacheReqFingerprint{})
	m.record(provider.TokenUsage{InputTokens: 5000, CacheRead: 5000}, cacheReqFingerprint{})

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
	}, cacheReqFingerprint{})
	if g != "" {
		t.Fatalf("single sample should not warn yet: %q", g)
	}
	// The stored sample's warm ratio must be 0.9, not 9000/19000=0.474:
	// drive to a verdict via the storm path (warm then cold burst) and
	// confirm warm was RECORDED - with the old double-count, warmSeen
	// could never set and the storm verdict was unreachable.
	m2 := newCacheEffMonitor()
	for i := 0; i < cacheEffMinCalls; i++ {
		m2.record(provider.TokenUsage{InputTokens: 10000, CacheRead: 9000, PromptTokensTotal: 10000}, cacheReqFingerprint{})
	}
	if !m2.warmSeen {
		t.Fatal("90% cache-hit samples never recorded warm - subset double-count regression")
	}
}

// driveToStorm warms the monitor with fp then feeds cold calls until the
// storm guidance fires, returning it.
func driveToStorm(t *testing.T, m *cacheEffMonitor, warmFP, coldFP cacheReqFingerprint) string {
	t.Helper()
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{InputTokens: 1000, CacheRead: 9000}, warmFP)
	}
	var guidance string
	for i := 0; i < cacheStormConsecutive+1; i++ {
		guidance = m.record(provider.TokenUsage{InputTokens: 10000, CacheRead: 0}, coldFP)
		if guidance != "" {
			break
		}
	}
	return guidance
}

// TestCacheEffMonitor_AttributesToolChurn pins the evidence-based bust
// attribution: a tool-hash delta between the last warm call and the bust is
// direct evidence the tools array broke the prefix, so the guidance must name
// it (with the definition counts) instead of the generic guess list.
func TestCacheEffMonitor_AttributesToolChurn(t *testing.T) {
	m := newCacheEffMonitor()
	guidance := driveToStorm(t, m,
		cacheReqFingerprint{sysHash: 111, toolHash: 100, toolCount: 12},
		cacheReqFingerprint{sysHash: 111, toolHash: 200, toolCount: 14})
	if guidance == "" {
		t.Fatal("expected storm guidance")
	}
	if !strings.Contains(guidance, "Attributed cause: tool definitions changed") {
		t.Errorf("expected tool-churn attribution, got: %s", guidance)
	}
	if !strings.Contains(guidance, "12 -> 14 definitions") {
		t.Errorf("expected definition-count evidence, got: %s", guidance)
	}
	if !strings.Contains(guidance, "Other possible causes") {
		t.Errorf("generic list should be demoted after attribution, got: %s", guidance)
	}
}

// TestCacheEffMonitor_AttributesSystemPromptChange: a system-prompt hash
// delta with a stable tool hash must attribute to the system prompt.
func TestCacheEffMonitor_AttributesSystemPromptChange(t *testing.T) {
	m := newCacheEffMonitor()
	guidance := driveToStorm(t, m,
		cacheReqFingerprint{sysHash: 111, toolHash: 100, toolCount: 12},
		cacheReqFingerprint{sysHash: 222, toolHash: 100, toolCount: 12})
	if guidance == "" {
		t.Fatal("expected storm guidance")
	}
	if !strings.Contains(guidance, "Attributed cause: system prompt changed") {
		t.Errorf("expected system-prompt attribution, got: %s", guidance)
	}
	if strings.Contains(guidance, "tool definitions changed") {
		t.Errorf("tool hash was stable; must not attribute to tools, got: %s", guidance)
	}
}

// TestCacheEffMonitor_AttributesIdleExpiry: identical fingerprints plus a
// gap >= cacheIdleTTL between the last warm call and the first cold call must
// attribute to natural TTL expiry, not instability.
func TestCacheEffMonitor_AttributesIdleExpiry(t *testing.T) {
	m := newCacheEffMonitor()
	fp := cacheReqFingerprint{sysHash: 111, toolHash: 100, toolCount: 12}
	for i := 0; i < cacheEffMinCalls; i++ {
		m.record(provider.TokenUsage{InputTokens: 1000, CacheRead: 9000}, fp)
	}
	// Simulate an idle gap: the warm call completed 10 minutes ago.
	m.lastWarmAt = time.Now().Add(-10 * time.Minute)
	guidance := driveToStormColdOnly(t, m, fp)
	if guidance == "" {
		t.Fatal("expected storm guidance")
	}
	if !strings.Contains(guidance, "Attributed cause: idle gap") {
		t.Errorf("expected idle-expiry attribution, got: %s", guidance)
	}
	if !strings.Contains(guidance, "expired naturally") {
		t.Errorf("expected expiry wording, got: %s", guidance)
	}
	if strings.Contains(guidance, "changed") {
		t.Errorf("fingerprints identical; must not attribute to a change, got: %s", guidance)
	}
}

// driveToStormColdOnly feeds cold calls (same fingerprint as warm) until the
// storm fires; used by the idle-expiry test.
func driveToStormColdOnly(t *testing.T, m *cacheEffMonitor, fp cacheReqFingerprint) string {
	t.Helper()
	var guidance string
	for i := 0; i < cacheStormConsecutive+1; i++ {
		guidance = m.record(provider.TokenUsage{InputTokens: 10000, CacheRead: 0}, fp)
		if guidance != "" {
			break
		}
	}
	return guidance
}

// TestCacheEffMonitor_UnattributedKeepsGuessList: identical fingerprints and
// a short gap leave the bust without evidence; the guidance must keep the
// generic cause list instead of inventing an attribution.
func TestCacheEffMonitor_UnattributedKeepsGuessList(t *testing.T) {
	m := newCacheEffMonitor()
	fp := cacheReqFingerprint{sysHash: 111, toolHash: 100, toolCount: 12}
	guidance := driveToStorm(t, m, fp, fp)
	if guidance == "" {
		t.Fatal("expected storm guidance")
	}
	if strings.Contains(guidance, "Attributed cause:") {
		t.Errorf("no evidence observed; must stay unattributed, got: %s", guidance)
	}
	if !strings.Contains(guidance, "Likely causes and fixes:") {
		t.Errorf("expected generic cause list, got: %s", guidance)
	}
}

// TestFingerprintToolDefs: the fingerprint must change when any request-
// visible part of a tool definition changes, and stay stable otherwise.
func TestFingerprintToolDefs(t *testing.T) {
	base := []provider.ToolDefinition{
		{Name: "read_file", Description: "read", Parameters: []byte(`{"type":"object"}`)},
		{Name: "edit_file", Description: "edit", Parameters: []byte(`{"type":"object"}`)},
	}
	h1, n1 := fingerprintToolDefs(base)
	h2, n2 := fingerprintToolDefs(base)
	if h1 != h2 || n1 != n2 || n1 != 2 {
		t.Fatalf("fingerprint not deterministic: %d/%d vs %d/%d", h1, n1, h2, n2)
	}

	renamed := append([]provider.ToolDefinition(nil), base...)
	renamed[1].Name = "write_file"
	if h3, _ := fingerprintToolDefs(renamed); h3 == h1 {
		t.Error("renaming a tool must change the fingerprint")
	}

	reschema := append([]provider.ToolDefinition(nil), base...)
	reschema[0].Parameters = []byte(`{"type":"object","properties":{}}`)
	if h4, _ := fingerprintToolDefs(reschema); h4 == h1 {
		t.Error("schema change must change the fingerprint")
	}

	added := append(append([]provider.ToolDefinition(nil), base...),
		provider.ToolDefinition{Name: "grep", Parameters: []byte(`{}`)})
	if h5, n5 := fingerprintToolDefs(added); h5 == h1 || n5 != 3 {
		t.Errorf("added tool must change hash and count, got %d/%d", h5, n5)
	}
}
