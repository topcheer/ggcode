package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// maybeInjectDynamicSystemPrompt calls the systemPromptInjector callback
// (if set) and appends the returned text to the base system prompt.
// The base prompt is always restored first, so dynamic content never
// accumulates across runs.
func (a *Agent) maybeInjectDynamicSystemPrompt() {
	a.mu.Lock()
	fn := a.systemPromptInjector
	base := a.baseSystemPrompt
	a.mu.Unlock()

	if fn == nil {
		return
	}

	extra := strings.TrimSpace(fn())

	newText := base
	if extra != "" {
		newText = base + "\n\n" + extra
	}

	cm, ok := a.contextManager.(*context.Manager)
	if !ok {
		return
	}
	cm.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: newText}},
	})
}
