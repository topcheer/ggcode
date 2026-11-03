package agent

import (
	"context"

	"github.com/topcheer/ggcode/internal/provider"
)

func (a *Agent) HealthCheck(ctx context.Context) error {
	if a.provider == nil {
		return nil
	}
	messages := []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "ping"}},
	}}
	_, err := a.auxChat(ctx, messages, nil)
	return err
}
