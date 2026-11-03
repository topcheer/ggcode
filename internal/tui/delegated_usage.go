package tui

import "github.com/topcheer/ggcode/internal/provider"

func (r *REPL) recordDelegatedUsage(usage provider.TokenUsage) {
	r.recordSessionUsage(usage, "subagent")
	if a := r.model.agent; a != nil {
		a.RecordDelegatedTokenUsage(int64(usage.InputTokens), int64(usage.OutputTokens))
	}
}
