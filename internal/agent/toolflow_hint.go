package agent

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
	toolflowHintMinCalls          = 2
	toolflowCacheTTL              = 15 * time.Minute
	toolflowHintMaxPatterns       = 10
	toolflowHintMinSupport        = 5
	toolflowHintMinConfidence     = 0.7
	toolflowConsolidateMinSupport = 10
)

var (
	toolflowCacheMu    sync.Mutex
	toolflowCacheReady bool
	toolflowCacheAt    time.Time
	toolflowCachePats  []memory.ToolFlowPattern
	toolflowMine       = memory.AnalyzeToolFlows
)

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

		if best == nil || len(p.Prefix) > len(best.Prefix) ||
			(len(p.Prefix) == len(best.Prefix) && p.Confidence > best.Confidence) {
			best = p
		}
	}
	if best == nil {
		return ""
	}
	a.toolflowHintFired = true

	out := fmt.Sprintf("## Established Workflow Hint (statistical, reference only)\nYour recent tool sequence %s matches a recurring workflow in your past sessions (support=%d, confidence=%.0f%%), which usually continues with `%s`. Reference data from your own usage history - follow it only if it fits the current task.\n",
		strings.Join(best.Prefix, " -> "), best.Count, best.Confidence*100, best.Next)
	out += a.toolflowConsolidationAdvice(best)
	return out
}

func (a *Agent) toolflowConsolidationAdvice(p *memory.ToolFlowPattern) string {
	if p.Count < toolflowConsolidateMinSupport {
		return ""
	}
	involved := strings.Join(append(append([]string{}, p.Prefix...), p.Next), " ")
	if !strings.Contains(involved, "run_command") {
		return ""
	}
	return fmt.Sprintf("This exact run_command sequence has recurred %d times. If the underlying shell commands are stable across sessions, consider `cmd_snippet save` to fold them into one reusable snippet - reference-level suggestion, not an instruction.\n", p.Count)
}

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
