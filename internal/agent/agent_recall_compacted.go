package agent

import (
	"fmt"
	"strings"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

type recallCompactedSearcher interface {
	SearchCompactedArchive(query string, limit int) []ctxpkg.CompactedHit
}

const defaultRecallLimit = 5

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
		return ""
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
