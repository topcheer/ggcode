package tui

import "github.com/topcheer/ggcode/internal/provider"

// recordDelegatedUsage records sub-agent token spend in both the session
// ledger (display/JSONL, as before) and the parent agent's session token
// budget ladder (delegation budgets: sub-agent spend must count toward the
// 80/95/100% thresholds, not bypass them). Wired as the OnUsage callback for
// spawn_agent / use_namedagent in repl.go.
func (r *REPL) recordDelegatedUsage(usage provider.TokenUsage) {
	r.recordSessionUsage(usage, "subagent")
	if a := r.model.agent; a != nil {
		a.RecordDelegatedTokenUsage(int64(usage.InputTokens), int64(usage.OutputTokens))
	}
}
