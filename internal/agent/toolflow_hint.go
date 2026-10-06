package agent

// r484: Operational memory / next-action proactivity. The mined toolflow
// store (r449) answered "which sequences does this user repeat" only when
// the model chose to call recall_toolflow. This hint is the harness-side
// half: once per run, when the tools executed so far end with a mined
// high-confidence prefix, surface the statistically dominant continuation
// as reference data - the "single highest-leverage next action" pattern
// (proactive memory agents: escalate with evidence instead of waiting to
// be asked). One line, one shot, zero cost on cold stores.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

const (
	// toolflowHintMinCalls: prefixes are 1-2 grams; a shorter history only
	// supports 1-gram matches which are too generic to be actionable.
	toolflowHintMinCalls = 2
	// toolflowCacheTTL (#3436): mining reads up to toolFlowScanSessionCap
	// session files whole (multi-GB on heavy installs). The r484 wiring calls
	// this before EVERY tool batch, so an uncached miss path re-scanned the
	// store per batch. Patterns evolve only as sessions accumulate, so a
	// process-wide TTL cache amortizes mining to once per window.
	toolflowCacheTTL = 15 * time.Minute
	// toolflowHintMaxPatterns caps the mining query (same default cap as
	// the recall_toolflow tool's upper bound).
	toolflowHintMaxPatterns = 10
	// Mirrors the store-side gates (memory/toolflow.go): support >= 5
	// sessions-occurrences, confidence >= 0.7.
	toolflowHintMinSupport    = 5
	toolflowHintMinConfidence = 0.7
)

var (
	toolflowCacheMu    sync.Mutex
	toolflowCacheReady bool
	toolflowCacheAt    time.Time
	toolflowCachePats  []memory.ToolFlowPattern
	// toolflowMine is the mining entry point as a function variable purely
	// so tests can count/expiry-inject without touching the real store
	// (#3436).
	toolflowMine = memory.AnalyzeToolFlows
)

// toolflowCachedPatterns returns the mined patterns, mining at most once
// per toolflowCacheTTL window per process (#3436). Errors and empty stores
// also arm the cache: a failing or cold store must not be re-scanned every
// batch for the rest of the window.
func toolflowCachedPatterns() []memory.ToolFlowPattern {
	toolflowCacheMu.Lock()
	defer toolflowCacheMu.Unlock()
	if toolflowCacheReady && time.Since(toolflowCacheAt) < toolflowCacheTTL {
		return toolflowCachePats
	}
	toolflowCacheReady = true
	toolflowCacheAt = time.Now()
	home, err := os.UserHomeDir()
	if err != nil {
		toolflowCachePats = nil
		return nil
	}
	pats, err := toolflowMine(filepath.Join(home, ".ggcode", "sessions"), toolflowHintMaxPatterns)
	if err != nil {
		toolflowCachePats = nil
		return nil
	}
	toolflowCachePats = pats
	return pats
}

// maybeToolflowSuggestion checks the run's executed-tool sequence against
// the mined workflow patterns and returns a single reference-level hint
// line when the sequence tail matches a high-confidence prefix. One shot
// per run (a.toolflowHintFired); returns "" on repeat, mining failure, or
// no match. Mining itself is process-cached (#3436): the per-batch call
// only re-scans the session store once per TTL window.
func (a *Agent) maybeToolflowSuggestion(recentTools []string) string {
	if a.toolflowHintFired {
		return ""
	}
	if len(recentTools) < toolflowHintMinCalls {
		return ""
	}
	pats := toolflowCachedPatterns()
	if len(pats) == 0 {
		return ""
	}
	var best *memory.ToolFlowPattern
	for i := range pats {
		p := &pats[i]
		if p.Count < toolflowHintMinSupport || p.Confidence < toolflowHintMinConfidence {
			continue
		}
		if !toolflowPrefixMatchesSuffix(p.Prefix, recentTools) {
			continue
		}
		// Prefer the longest matching prefix (2-gram beats 1-gram); break
		// ties on confidence.
		if best == nil || len(p.Prefix) > len(best.Prefix) ||
			(len(p.Prefix) == len(best.Prefix) && p.Confidence > best.Confidence) {
			best = p
		}
	}
	if best == nil {
		return ""
	}
	a.toolflowHintFired = true
	// Reference-level wording (r483 discipline): mined statistics are the
	// user's own past usage, advisory only - never a standing instruction.
	return fmt.Sprintf("## Established Workflow Hint (statistical, reference only)\nYour recent tool sequence %s matches a recurring workflow in your past sessions (support=%d, confidence=%.0f%%), which usually continues with `%s`. Reference data from your own usage history - follow it only if it fits the current task.\n",
		strings.Join(best.Prefix, " -> "), best.Count, best.Confidence*100, best.Next)
}

// toolflowPrefixMatchesSuffix reports whether prefix is a contiguous
// suffix of seq (the run's most recent tools).
func toolflowPrefixMatchesSuffix(prefix, seq []string) bool {
	if len(prefix) == 0 || len(prefix) > len(seq) {
		return false
	}
	tail := seq[len(seq)-len(prefix):]
	for i := range prefix {
		if tail[i] != prefix[i] {
			return false
		}
	}
	return true
}
