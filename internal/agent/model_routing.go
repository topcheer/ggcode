package agent

import (
	"context"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (a *Agent) SetAuxModel(resolved *config.ResolvedEndpoint, model string) {
	if resolved == nil || model == "" || model == resolved.Model {
		return
	}
	clone := *resolved
	clone.Model = model
	a.auxResolved = &clone
	a.auxModelName = model
}

func (a *Agent) auxProviderFor() provider.Provider {
	if a.auxResolved == nil {
		return a.provider
	}
	if a.auxFailed {
		return a.provider
	}
	if a.auxProvider != nil {
		return a.auxProvider
	}
	p, err := provider.NewProvider(a.auxResolved)
	if err != nil {

		debug.Log("agent", "model routing: aux provider build failed for %q, falling back to main model: %v",
			a.auxModelName, err)
		a.auxFailed = true
		return a.provider
	}
	a.auxProvider = p
	debug.Log("agent", "model routing: auxiliary calls routed to %q (main: %q)",
		a.auxModelName, a.auxResolvedModelName())
	return p
}

func (a *Agent) auxResolvedModelName() string { return a.auxModelName }

func (a *Agent) auxChat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return a.auxProviderFor().Chat(ctx, messages, tools)
}
