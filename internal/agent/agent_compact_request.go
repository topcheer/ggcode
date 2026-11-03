package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

func (a *Agent) RequestAutoCompact(reason string) string {
	if reason != "" {
		debug.Log("agent", "compact_context requested: %s", reason)
	}
	a.mu.RLock()
	cm := a.contextManager
	prov := a.provider
	running := a.precompact != nil
	a.mu.RUnlock()
	if cm == nil || prov == nil {
		return "Context compaction unavailable: context manager or provider not ready."
	}

	var b strings.Builder
	if running {
		b.WriteString("Full summarization is already running in the background; its result applies at the next turn boundary. ")
	} else if threshold := cm.AutoCompactThreshold(); threshold > 0 && cm.TokenCount() >= threshold {
		a.StartPreCompact()
		b.WriteString("Usage is at/above the auto-compact threshold: full LLM summarization scheduled in the background and will apply at the next turn boundary. ")
	} else {
		b.WriteString("Usage is below the auto-compact threshold: full summarization not scheduled. ")
	}

	if m, ok := cm.(interface{ CompactSupersededReads() int }); ok {
		if freed := m.CompactSupersededReads(); freed > 0 {
			b.WriteString(fmt.Sprintf("Mechanical reclaim dropped superseded file reads (-%d tokens). ", freed))
		}
	}
	b.WriteString("Continue the task; no further action needed.")
	return b.String()
}
