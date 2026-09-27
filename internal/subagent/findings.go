package subagent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Findings handoff: bounded shared working memory across sub-agents
// (blackboard-style context sharing, cf. bMAS arXiv:2507.01701 and
// "Multi-Agent Memory from a Computer Architecture Perspective"
// arXiv:2603.10062, which identifies cache sharing across agents as a key
// protocol gap).
//
// Without a handoff channel, wave-2+ sub-agents re-do exploration their
// predecessors already finished, and once the parent agent's context is
// compacted or old tool results are cleared, earlier sub-agent results are
// unrecoverable — the only remaining channel is whatever the parent still
// remembers to paste into the next task string.
//
// The handoff is injected into the sub-agent's SYSTEM prompt at run start
// (see runner.go), so it never grows the parent's context and the payload
// is strictly bounded.
const (
	findingsMaxEntries     = 6    // most recent completed sub-agents
	findingsMaxResultRunes = 800  // per-result digest cap
	findingsMaxTaskRunes   = 120  // per-task one-liner cap
	findingsMaxTotalRunes  = 4800 // whole-section budget (header excluded)
	findingsTruncateSuffix = " …[truncated]"
)

// completedFindings returns a bounded, formatted section describing results
// of earlier completed sub-agents in this session (newest first), or "" when
// there is nothing to hand off. excludeID (the agent about to run) is skipped
// so an agent never sees its own entry.
func (m *Manager) completedFindings(excludeID string) string {
	type finding struct {
		name, task, result string
		endedAt            time.Time
	}
	m.mu.Lock()
	findings := make([]finding, 0, len(m.agents))
	for id, sa := range m.agents {
		if id == excludeID {
			continue
		}
		sa.mu.Lock()
		// Only successfully completed agents hand off findings. Failed,
		// cancelled, or still-running agents are excluded: their results may
		// be partial or misleading (Cancelled agents can carry backfilled
		// partial results, see #551-B).
		if sa.Status == StatusCompleted && sa.Result != "" {
			findings = append(findings, finding{
				name:    sa.Name,
				task:    sa.Task,
				result:  sa.Result,
				endedAt: sa.EndedAt,
			})
		}
		sa.mu.Unlock()
	}
	m.mu.Unlock()

	if len(findings) == 0 {
		return ""
	}
	// Newest first: the most recently completed agent is the most likely to
	// still be relevant to the task at hand.
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].endedAt.After(findings[j].endedAt)
	})
	if len(findings) > findingsMaxEntries {
		findings = findings[:findingsMaxEntries]
	}

	var b strings.Builder
	b.WriteString("\n\n=== FINDINGS FROM EARLIER SUB-AGENTS IN THIS SESSION ===\n")
	b.WriteString("Earlier sub-agents working in this same workspace produced the results below. Treat them as starting context that may save you exploration work; verify anything load-bearing before relying on it.\n")
	total := 0
	for i, f := range findings {
		var item strings.Builder
		fmt.Fprintf(&item, "[%d] %s — %s\n", i+1, f.name, truncateRunes(f.task, findingsMaxTaskRunes))
		item.WriteString(truncateRunes(f.result, findingsMaxResultRunes))
		item.WriteString("\n\n")
		s := item.String()
		runes := len([]rune(s))
		if total+runes > findingsMaxTotalRunes {
			break
		}
		b.WriteString(s)
		total += runes
	}
	return b.String()
}

// truncateRunes caps s at max runes, appending a truncation marker when cut.
// When the cut lands mid-line it backs off to the nearest line boundary so
// digests stay readable.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	quarter := max * 3 / 4
	cut := r[:max]
	if quarter > 0 {
		if idx := strings.LastIndex(string(r[quarter:max]), "\n"); idx >= 0 {
			cut = r[:quarter+idx]
		}
	}
	return string(cut) + findingsTruncateSuffix
}
