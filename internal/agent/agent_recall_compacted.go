package agent

// RecallCompacted powers the recall_compacted tool: re-retrieval of
// conversation content removed by compaction (Context Engineering,
// Anthropic 2025 — "compaction is necessary but not sufficient").
//
// The agent's contextManager is the ctxpkg.ContextManager interface; the
// search entry point is intentionally NOT added to that interface (mock
// implementers stay untouched). Instead we bind via a local interface
// assertion against the concrete hit type, the same late-binding pattern
// RequestAutoCompact uses for CompactSupersededReads.

import (
	"fmt"
	"strings"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

// recallCompactedSearcher is the locally-asserted capability: implemented
// by *ctxpkg.Manager, invisible to ContextManager mocks.
type recallCompactedSearcher interface {
	SearchCompactedArchive(query string, limit int) []ctxpkg.CompactedHit
}

// defaultRecallLimit caps hits per call; the tool's default matches.
const defaultRecallLimit = 5

// RecallCompacted searches the owning agent's archive of compacted-away
// content and returns a model-readable result body. Empty string means
// no matches (or no archive). Bound to the recall_compacted tool by cmd
// wiring (agentruntime.RegisterRecallCompactedTool).
func (a *Agent) RecallCompacted(query string, limit int) string {
	if limit <= 0 {
		limit = defaultRecallLimit
	}
	a.mu.RLock()
	cm := a.contextManager
	a.mu.RUnlock()
	if cm == nil {
		return "Compacted-content archive unavailable: context manager not ready."
	}
	s, ok := cm.(recallCompactedSearcher)
	if !ok {
		return "" // context manager has no archive (e.g. test mock)
	}
	hits := s.SearchCompactedArchive(query, limit)
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d archived match(es) for %q (newest first):\n", len(hits), query)
	for i, h := range hits {
		fmt.Fprintf(&b, "\n[%d] role=%s", i+1, h.Role)
		if h.ToolID != "" {
			b.WriteString(" tool=" + h.ToolID)
		}
		if h.Reason != "" {
			b.WriteString(" dropped-by=" + h.Reason)
		}
		b.WriteString("\n")
		b.WriteString(h.Excerpt)
		b.WriteString("\n")
	}
	b.WriteString("\nThese are verbatim excerpts of content that was compacted away; the live conversation no longer contains them.")
	return b.String()
}
