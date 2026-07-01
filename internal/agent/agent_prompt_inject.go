package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// maybeInjectDynamicSystemPrompt calls the systemPromptInjector callback
// (if set) and appends the returned text to the current system message.
// This is restored before the next run by tracking the original prompt.
//
// Use case: when lanchat peers are active in the same workspace, inject
// a warning telling the LLM to check for file conflicts before editing.
func (a *Agent) maybeInjectDynamicSystemPrompt() {
	a.mu.Lock()
	fn := a.systemPromptInjector
	a.mu.Unlock()

	if fn == nil {
		return
	}

	extra := strings.TrimSpace(fn())
	if extra == "" {
		return
	}

	cm, ok := a.contextManager.(*context.Manager)
	if !ok {
		return
	}

	msgs := cm.Messages()
	for i, msg := range msgs {
		if msg.Role != "system" {
			continue
		}
		// Check if already injected (avoid double-inject on re-entry)
		for _, block := range msg.Content {
			if block.Type == "text" && strings.Contains(block.Text, extra) {
				return // already present
			}
		}
		// Append the extra text to the existing system message
		var newText string
		for _, block := range msg.Content {
			if block.Type == "text" {
				newText = block.Text
				break
			}
		}
		newText = newText + "\n\n" + extra
		updated := provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: newText}},
		}
		cm.UpdateFirstSystemMessage(updated)
		_ = i
		return
	}
}
