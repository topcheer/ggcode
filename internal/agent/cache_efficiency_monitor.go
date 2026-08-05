package agent

// Prompt Cache Efficiency Monitor - Context Engineering Intelligence
//
// Research basis: Anthropic's Context Engineering guide (2025) emphasizes prompt
// cache stability as a primary cost lever. The cache prefix (system prompt +
// early conversation) is cached with a 5-minute sliding TTL. When the prefix
// changes - even by a single token - the ENTIRE cache is invalidated and the
// full prefix is re-written at 1.25x cost on the next call.
//
// Common cache-busting patterns in coding agents:
//   1. Dynamic tool pruning: adding/removing tools mid-run changes the tools
//      array in the API request, busting the cache from the tools breakpoint.
//   2. Adaptive effort: changing reasoning_effort per-turn alters the request
//      metadata, potentially busting cache if the provider includes it in the
//      prefix hash.
//   3. Pinned context updates: injecting/updating pinned context mid-conversation
//      modifies the system prompt, busting cache from the system breakpoint.
//   4. System prompt mutations: intelligence gates that inject advisory messages
//      into the system prompt cause cache invalidation on every injection.
//   5. Timestamp/nonce injection: some agents inject current timestamps into
//      the system prompt, busting cache on every single call.
//
// What this monitor does:
//   - Tracks cache_read / cache_write / input tokens per LLM call
//   - Computes a rolling cache hit ratio (cache_read / total_input)
//   - Detects "cache bust storms": consecutive calls where cache_read drops
//     to near-zero after previously being high, indicating prefix instability
//   - When a storm is detected, injects guidance identifying the likely cause
//
// This is different from:
//   - cache_keepalive.go: keeps the cache warm during IDLE periods (TTL-based)
//   - cache_efficiency_monitor.go (this): detects cache INSTABILITY during
//     ACTIVE runs (prefix-bust-based)
//
// Zero LLM cost - deterministic token arithmetic + rolling window analysis.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// cacheEffWindow is the rolling window size for efficiency analysis.
	cacheEffWindow = 8

	// cacheEffMinCalls: minimum calls before analysis activates.
	cacheEffMinCalls = 4

	// cacheHitRatioThreshold: calls above this ratio are considered "cache warm".
	cacheHitRatioThreshold = 0.50

	// cacheBustRatioThreshold: calls below this ratio are considered "cache cold".
	cacheBustRatioThreshold = 0.15

	// cacheStormConsecutive: number of consecutive cold calls (after warm)
	// that triggers a storm alert.
	cacheStormConsecutive = 3

	// cacheEffWarnOnce: fire at most once per run to avoid nagging.
	// After the first alert, the root cause guidance has been delivered.
)

// cacheEffSample records cache metrics for a single LLM call.
type cacheEffSample struct {
	input     int // raw input tokens (non-cached)
	cacheRead int // tokens served from cache
	total     int // input + cacheRead (total prompt size)
}

// cacheEffMonitor tracks prompt cache efficiency across an agent run and
// detects cache invalidation storms.
type cacheEffMonitor struct {
	mu sync.Mutex

	samples    []cacheEffSample // rolling window of recent calls
	warmSeen   bool             // true once we've observed a high-cache-hit call
	coldStreak int              // consecutive cold calls since last warm call
	alerted    bool             // fired alert this run
}

func newCacheEffMonitor() *cacheEffMonitor {
	return &cacheEffMonitor{
		samples: make([]cacheEffSample, 0, cacheEffWindow+1),
	}
}

// reset clears state for a new run.
func (m *cacheEffMonitor) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = m.samples[:0]
	m.warmSeen = false
	m.coldStreak = 0
	m.alerted = false
}

// record tracks a new LLM call's cache metrics. Returns guidance if a cache
// bust storm is detected.
func (m *cacheEffMonitor) record(usage provider.TokenUsage) string {

	m.mu.Lock()
	defer m.mu.Unlock()

	sample := cacheEffSample{
		input:     usage.InputTokens,
		cacheRead: usage.CacheRead,
		total:     usage.InputTokens + usage.CacheRead,
	}

	// Append to rolling window
	m.samples = append(m.samples, sample)
	if len(m.samples) > cacheEffWindow {
		m.samples = m.samples[1:]
	}

	if m.alerted {
		return "" // already fired this run
	}

	if len(m.samples) < cacheEffMinCalls {
		return "" // not enough data
	}

	// Skip analysis if provider doesn't use caching at all.
	hasCacheActivity := false
	for _, samp := range m.samples {
		if samp.cacheRead > 0 {
			hasCacheActivity = true
			break
		}
	}
	if !hasCacheActivity {
		return ""
	}

	// Compute hit ratio for this sample
	ratio := m.hitRatio(sample)

	if ratio >= cacheHitRatioThreshold {
		m.warmSeen = true
		m.coldStreak = 0
	} else if ratio <= cacheBustRatioThreshold {
		if m.warmSeen {
			m.coldStreak++
		}
	} else {
		// Intermediate ratio — don't increment cold streak but don't reset either
	}

	if m.coldStreak >= cacheStormConsecutive {
		m.alerted = true
		guidance := m.formatStormGuidance()
		debug.Log("cache-efficiency", "cache bust storm detected: coldStreak=%d window=%s", m.coldStreak, m.windowSummary())
		return guidance
	}

	return ""
}

// hitRatio computes the cache hit ratio for a single sample.
func (m *cacheEffMonitor) hitRatio(s cacheEffSample) float64 {
	if s.total == 0 {
		return 0
	}
	return float64(s.cacheRead) / float64(s.total)
}

// formatStormGuidance produces actionable guidance when a cache bust storm
// is detected.
func (m *cacheEffMonitor) formatStormGuidance() string {
	var sb strings.Builder

	sb.WriteString("[Cache Efficiency Alert] Prompt cache is being repeatedly invalidated. ")
	sb.WriteString(fmt.Sprintf("Cache hit ratio dropped from warm to ~0%% over %d consecutive calls. ", m.coldStreak))
	sb.WriteString("This means the API is re-processing the full system prompt + conversation prefix each turn, ")
	sb.WriteString("costing significantly more tokens (1.25x for cache writes vs 0.1x for reads).\n\n")
	sb.WriteString("Likely causes and fixes:\n")
	sb.WriteString("  - System prompt instability: intelligence gates or dynamic injections are modifying the system prompt each turn. ")
	sb.WriteString("Move advisory messages to user/tool messages instead of system prompt.\n")
	sb.WriteString("  - Tool list churn: adding/removing tools mid-run busts cache from the tools breakpoint. ")
	sb.WriteString("Stabilize the tool set early in the run.\n")
	sb.WriteString("  - Pinned context updates: injecting new pinned items modifies the system prompt prefix. ")
	sb.WriteString("Batch pin operations rather than adding one item per turn.\n")
	sb.WriteString("  - Reasoning effort changes: frequent effort level switches may affect cache stability on some providers.\n\n")
	sb.WriteString(fmt.Sprintf("Recent window: %s", m.windowSummary()))

	return sb.String()
}

// windowSummary returns a compact summary of the current window for diagnostics.
func (m *cacheEffMonitor) windowSummary() string {
	if len(m.samples) == 0 {
		return "(empty)"
	}
	var sb strings.Builder
	for idx, samp := range m.samples {
		if idx > 0 {
			sb.WriteString(" -> ")
		}
		ratio := 0.0
		if samp.total > 0 {
			ratio = float64(samp.cacheRead) / float64(samp.total) * 100
		}
		fmt.Fprintf(&sb, "[%d: in=%d cr=%d %.0f%%]", idx, samp.input, samp.cacheRead, ratio)
	}
	return sb.String()
}
