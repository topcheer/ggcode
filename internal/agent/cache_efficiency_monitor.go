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
//   - When a storm is detected, ATTRIBUTES the cause from evidence: each call
//     carries a fingerprint of the request prefix composition (system prompt
//     hash + tool definitions hash + tool count), so a delta between the last
//     warm call and the bust names the part that actually changed. A gap
//     longer than the provider TTL before the first cold call is attributed
//     to natural expiry instead. Without evidence the guidance stays
//     unattributed rather than guessing.
//
// This is different from:
//   - cache_keepalive.go: keeps the cache warm during IDLE periods (TTL-based)
//   - cache_efficiency_monitor.go (this): detects cache INSTABILITY during
//     ACTIVE runs (prefix-bust-based) and explains WHY the prefix broke
//
// Zero LLM cost - deterministic token arithmetic + rolling window analysis.

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

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

	// cacheIdleTTL approximates the sliding TTL most providers apply to the
	// prompt cache (Anthropic documents 5 minutes). When the gap between the
	// last warm call and the first cold call reaches this length, the bust is
	// attributed to natural expiry of the cached prefix, not to instability.
	cacheIdleTTL = 5 * time.Minute

	// cacheEffWarnOnce: fire at most once per run to avoid nagging.
	// After the first alert, the root cause guidance has been delivered.
)

// cacheEffSample records cache metrics for a single LLM call.
type cacheEffSample struct {
	input     int       // raw input tokens (non-cached)
	cacheRead int       // tokens served from cache
	total     int       // input + cacheRead (total prompt size)
	sysHash   uint64    // fnv hash of the system prompt sent with this call
	toolHash  uint64    // fnv hash of the tool definitions sent with this call
	toolCount int       // number of tool definitions sent with this call
	at        time.Time // completion time of the call
}

// cacheReqFingerprint captures the request-prefix composition that providers
// hash into the prompt-cache key: the system prompt and the tool definitions.
// Two consecutive calls with identical fingerprints share the same cacheable
// prefix shape; a delta between the last warm call and a cold call is direct
// evidence for the bust cause. The zero value means "fingerprint unknown"
// (e.g. tests) and disables attribution rather than producing false positives.
type cacheReqFingerprint struct {
	sysHash   uint64
	toolHash  uint64
	toolCount int
}

// hashCacheString returns a stable fnv hash of a request prefix component.
func hashCacheString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// fingerprintToolDefs hashes the parts of each tool definition that providers
// serialize into the request's tools array (name, description, JSON schema,
// allowed callers). Returns the hash and the definition count for evidence
// messages.
func fingerprintToolDefs(defs []provider.ToolDefinition) (uint64, int) {
	h := fnv.New64a()
	for _, d := range defs {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", d.Name, d.Description)
		_, _ = h.Write(d.Parameters)
		_, _ = fmt.Fprintf(h, "\x00%d", len(d.AllowedCallers))
		for _, c := range d.AllowedCallers {
			_, _ = fmt.Fprintf(h, "\x00%s", c)
		}
		_, _ = fmt.Fprint(h, "\x1e")
	}
	return h.Sum64(), len(defs)
}

// cacheEffMonitor tracks prompt cache efficiency across an agent run and
// detects cache invalidation storms.
type cacheEffMonitor struct {
	mu sync.Mutex

	samples    []cacheEffSample // rolling window of recent calls
	warmSeen   bool             // true once we've observed a high-cache-hit call
	coldStreak int              // consecutive cold calls since last warm call
	alerted    bool             // fired alert this run

	hasLastWarm bool                // true once lastWarmFP/lastWarmAt are meaningful
	lastWarmFP  cacheReqFingerprint // request fingerprint at the last warm call
	lastWarmAt  time.Time           // completion time of the last warm call
	firstColdAt time.Time           // completion time of the first cold call in the current streak
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
	m.hasLastWarm = false
	m.lastWarmFP = cacheReqFingerprint{}
	m.lastWarmAt = time.Time{}
	m.firstColdAt = time.Time{}
}

// record tracks a new LLM call's cache metrics. Returns guidance if a cache
// bust storm is detected.
func (m *cacheEffMonitor) record(usage provider.TokenUsage, fp cacheReqFingerprint) string {

	m.mu.Lock()
	defer m.mu.Unlock()

	// #1441-B: OpenAI-compat semantics make CacheRead a SUBSET of
	// InputTokens (openai.go: PromptTokens already includes
	// CachedTokens). The raw sum double-counted the cached portion:
	// warm's >=0.5 ratio became mathematically unreachable (C/(P0+2C)
	// with a real 90% hit computes 0.474), so warmSeen never set and the
	// storm detector stayed no-op on the mainline providers
	// (openrouter/deepseek/glm). provider.DisplayInputTokens already
	// performs exactly this normalization for display - reuse it so the
	// monitor sees the same numbers the user does.
	sample := cacheEffSample{
		input:     usage.InputTokens,
		cacheRead: usage.CacheRead,
		total:     usage.DisplayInputTokens() + usage.CacheRead,
		sysHash:   fp.sysHash,
		toolHash:  fp.toolHash,
		toolCount: fp.toolCount,
		at:        time.Now(),
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
		m.hasLastWarm = true
		m.lastWarmFP = fp
		m.lastWarmAt = sample.at
		m.firstColdAt = time.Time{}
	} else if ratio <= cacheBustRatioThreshold {
		if m.warmSeen {
			if m.coldStreak == 0 {
				m.firstColdAt = sample.at
			}
			m.coldStreak++
		}
	} else {
		// Intermediate ratio — don't increment cold streak but don't reset either
	}

	if m.coldStreak >= cacheStormConsecutive {
		m.alerted = true
		guidance := m.formatStormGuidance()
		debug.Log("cache-efficiency", "cache bust storm detected: coldStreak=%d cause=%q window=%s",
			m.coldStreak, m.bustCause(), m.windowSummary())
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

// bustCause inspects recorded evidence and attributes the bust to a specific
// mechanism. Comparison is between the triggering (cold) sample and the last
// warm sample's fingerprint: a hash delta is direct evidence that the cached
// prefix composition changed, and the gap between the last warm call and the
// first cold call distinguishes natural TTL expiry. Zero hashes mean the
// fingerprint was unavailable, in which case nothing is attributed rather
// than guessed. Returns "" when no mechanism has supporting evidence.
func (m *cacheEffMonitor) bustCause() string {
	if len(m.samples) == 0 {
		return ""
	}
	cur := m.samples[len(m.samples)-1]

	if m.hasLastWarm && m.lastWarmFP.toolHash != 0 && cur.toolHash != 0 &&
		cur.toolHash != m.lastWarmFP.toolHash {
		return fmt.Sprintf("Attributed cause: tool definitions changed after the last warm call "+
			"(%d -> %d definitions) - the request's tools array no longer matches the cached prefix, "+
			"so the bust starts at the tools breakpoint. Stabilize the tool set early in the run "+
			"(defer MCP reconnects, avoid mid-run enable/disable).",
			m.lastWarmFP.toolCount, cur.toolCount)
	}
	if m.hasLastWarm && m.lastWarmFP.sysHash != 0 && cur.sysHash != 0 &&
		cur.sysHash != m.lastWarmFP.sysHash {
		return "Attributed cause: system prompt changed after the last warm call - a dynamic " +
			"injection or pinned-context update mutated the cached prefix. Move per-turn advisory " +
			"text into user/tool messages instead of the system prompt."
	}
	if m.hasLastWarm && !m.lastWarmAt.IsZero() && !m.firstColdAt.IsZero() &&
		m.firstColdAt.Sub(m.lastWarmAt) >= cacheIdleTTL {
		return fmt.Sprintf("Attributed cause: idle gap of %s between the last warm call and the first "+
			"cold call exceeded the ~%s prompt-cache TTL - the prefix expired naturally. No instability "+
			"in the run itself; the cold calls simply rebuilt the cache.",
			m.firstColdAt.Sub(m.lastWarmAt).Truncate(time.Second), cacheIdleTTL)
	}
	return ""
}

// formatStormGuidance produces actionable guidance when a cache bust storm
// is detected.
func (m *cacheEffMonitor) formatStormGuidance() string {
	var sb strings.Builder

	sb.WriteString("[Cache Efficiency Alert] Prompt cache is being repeatedly invalidated. ")
	sb.WriteString(fmt.Sprintf("Cache hit ratio dropped from warm to ~0%% over %d consecutive calls. ", m.coldStreak))
	sb.WriteString("This means the API is re-processing the full system prompt + conversation prefix each turn, ")
	sb.WriteString("costing significantly more tokens (1.25x for cache writes vs 0.1x for reads).\n\n")

	if cause := m.bustCause(); cause != "" {
		sb.WriteString(cause)
		sb.WriteString("\n\nOther possible causes (no direct evidence this window):\n")
	} else {
		sb.WriteString("Likely causes and fixes:\n")
	}
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
