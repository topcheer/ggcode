package agent

import (
	"context"

	"github.com/topcheer/ggcode/internal/provider"
)

// HealthCheck performs a minimal LLM call to test whether the currently
// configured model is usable. Used by node health reporting (e.g. lanchat
// presence) to probe recovery after quota/rate-limit/auth failures.
//
// A tiny single-message Chat request is used rather than a models-list
// endpoint because coding plan quota errors only surface on the completion
// path — a list call may succeed while completions still fail.
func (a *Agent) HealthCheck(ctx context.Context) error {
	if a.provider == nil {
		return nil // no provider configured; nothing to probe
	}
	messages := []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "ping"}},
	}}
	_, err := a.provider.Chat(ctx, messages, nil)
	return err
}
