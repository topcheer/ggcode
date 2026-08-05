package agent

// Cache Efficiency Gate
//
// Research basis: Prompt caching (Anthropic, Google Gemini, OpenAI automatic
// caching) can reduce token costs by up to 90%. But caching has a hidden cost:
// cache writes are typically priced at 1.25x input rate while cache reads cost
// only 0.10x. If the agent frequently invalidates the cache prefix (changing
// system prompt, reordering messages, sending diverse tool calls), it pays the
// write premium repeatedly without benefiting from reads.
//
// This "cache thrashing" pattern is analogous to CPU cache thrashing or
// database page thrashing: the cost of maintenance exceeds the benefit.
//
// Industry research:
//   - Helicone (LLM observability): tracks cache hit rate and flags sessions
//     where write-to-read ratio is abnormally high
//   - Braintrust (evaluation platform): cost-per-call analysis includes cache
//     write overhead attribution
//   - Anthropic prompt caching docs: "cache misses are expensive; minimize
//     changes to the cached prefix"
//   - Aider --cache-keepalive-pings: proactively keeps cache warm
//
// Competitor gap: No major AI coding agent (Claude Code, Cursor, Aider,
// OpenHands, Devin) detects cache thrashing at runtime. They either display
// raw token counts or nothing at all.
//
// Our approach: After each API call that reports cache tokens, check if the
// session's write-to-read ratio indicates thrashing. If so, inject a targeted
// guidance message suggesting the agent minimize cache-prefix changes.
// Deterministic, zero LLM cost.
//
// Threshold rationale:
//   - Write-to-read ratio > 10: severe thrashing. For every 1 token read from
//     cache, 10+ were written. At typical 1.25x write / 0.10x read pricing,
//     this means the cache is net-negative (costing more than it saves).
//   - Minimum absolute thresholds (5000 cache writes, 1000 cache reads) prevent
//     false positives in early session turns where small token counts produce
//     noisy ratios.
//   - Fires at most once per run to avoid nagging.

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/cost"
)

// cacheEffGateState tracks cache token accumulation and detects thrashing.
type cacheEffGateState struct {
	mu        sync.Mutex
	tracker   *cost.Tracker
	warned    bool
	lastRead  int64
	lastWrite int64
	lastInput int64
}

// newCacheEffGateState creates a new cache efficiency gate.
// tracker may be nil; the gate becomes a no-op in that case.
func newCacheEffGateState(tracker *cost.Tracker) *cacheEffGateState {
	return &cacheEffGateState{
		tracker: tracker,
	}
}

// reset clears state for a new run.
func (g *cacheEffGateState) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.warned = false
	g.lastRead = 0
	g.lastWrite = 0
	g.lastInput = 0
}

// maybeWarnCacheThrashing checks the current cache write/read ratio and
// returns a non-empty guidance string if thrashing is detected.
// Returns "" if no warning is needed (no thrashing, already warned, or
// insufficient data).
func (g *cacheEffGateState) maybeWarnCacheThrashing() string {
	if g == nil || g.tracker == nil {
		return ""
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.warned {
		return ""
	}

	sc := g.tracker.SessionCost()

	// Need at least some cache activity to evaluate.
	minWrites := int64(5000) // Below this, ratios are noisy
	minReads := int64(1000)  // Below this, ratios are noisy
	thrashRatio := 10.0      // Writes > 10x reads = net-negative caching
	if sc.CacheWriteTokens < minWrites || sc.CacheReadTokens < minReads {
		return ""
	}

	// Compute incremental delta since last check.
	readDelta := sc.CacheReadTokens - g.lastRead
	writeDelta := sc.CacheWriteTokens - g.lastWrite

	g.lastRead = sc.CacheReadTokens
	g.lastWrite = sc.CacheWriteTokens

	// Compute incremental ratio if we have a previous baseline with meaningful
	// deltas (more sensitive to recent behavior); otherwise use cumulative.
	var ratio float64
	var ratioLabel string
	if readDelta > 0 && writeDelta > 0 {
		ratio = float64(writeDelta) / float64(readDelta)
		ratioLabel = "recent"
	} else {
		ratio = float64(sc.CacheWriteTokens) / float64(sc.CacheReadTokens)
		ratioLabel = "cumulative"
	}

	if ratio < thrashRatio {
		return ""
	}

	g.warned = true

	return fmt.Sprintf(
		"[Cache Thrashing Detected] Your prompt cache write-to-read ratio is %.1fx (%s). "+
			"You are writing %s tokens to cache but only reading back %s. "+
			"At typical cache pricing (1.25x write, 0.10x read), this pattern is costing MORE than caching saves. "+
			"Minimize changes to the cached prefix: keep the system prompt and early messages stable, "+
			"avoid reordering tool definitions, and batch similar operations to improve cache reuse.",
		ratio,
		ratioLabel,
		cost.FormatTokens(sc.CacheWriteTokens),
		cost.FormatTokens(sc.CacheReadTokens),
	)
}
