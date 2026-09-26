package agent

// RequestAutoCompact powers the compact_context tool (CAT - "Context as a
// Tool", ACL 2026 Findings): the model can reclaim context budget at
// semantically clean boundaries instead of waiting for the threshold cliff.
//
// Protocol safety: this NEVER rewrites conversation history synchronously in
// the tool-execution window - at that point the assistant's tool_use for
// compact_context has no tool_result yet, and a mid-flight Summarize could
// desync that pairing. Instead:
//   - the cheap mechanical pass (superseded-read reclaim) runs immediately;
//     it only rewrites content of OLD messages behind the context manager's
//     own lock, and is the same pass StartPreCompact runs;
//   - full LLM summarization goes through the existing background precompact
//     machinery (StartPreCompact), whose result is applied at the next turn
//     boundary by consumeReadyPreCompact - exactly how auto-compaction works.
//
// Per the clearing-tiers architecture note, proactive reclaim stays
// independent of the compaction handler: this entry point is agent-invoked
// and adds nothing to StartPreCompact itself.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// RequestAutoCompact is bound to the compact_context tool by cmd wiring.
// reason is the model-supplied rationale (logged for observability). The
// returned string is the tool-result body: a non-empty status line on every
// success path.
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

	// Mechanical reclaim is independent of the compaction handler and is
	// always safe to run: superseded reads are old file-read results whose
	// content was re-read later, so dropping them loses nothing current.
	if m, ok := cm.(interface{ CompactSupersededReads() int }); ok {
		if freed := m.CompactSupersededReads(); freed > 0 {
			b.WriteString(fmt.Sprintf("Mechanical reclaim dropped superseded file reads (-%d tokens). ", freed))
		}
	}
	b.WriteString("Continue the task; no further action needed.")
	return b.String()
}
