package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
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

// maybeInjectRatchetRules appends the top learned ratchet rules (by hit count)
// to the system prompt. This proactively surfaces lessons from previous runs
// so the agent knows about known pitfalls from the start, not just when a
// tool pattern reactively matches.
//
// Rules are appended after any dynamic system prompt content. The base
// prompt is always restored first, so rules never accumulate across runs.
func (a *Agent) maybeInjectRatchetRules() {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	rs := NewRuleStore(workingDir)
	if rs == nil {
		return
	}
	// Only inject the top 5 rules to keep system prompt concise.
	rulesText := rs.TopRulesForPrompt(5)
	if rulesText == "" {
		return
	}

	debug.Log("agent", "Injecting %d learned ratchet rules into system prompt")

	// Read the current system message (which may have been modified by
	// maybeInjectDynamicSystemPrompt) and append ratchet rules.
	cm, ok := a.contextManager.(*context.Manager)
	if !ok {
		return
	}
	msgs := cm.Messages()
	if len(msgs) == 0 || msgs[0].Role != "system" {
		return
	}
	currentText := ""
	if len(msgs[0].Content) > 0 && msgs[0].Content[0].Type == "text" {
		currentText = msgs[0].Content[0].Text
	}
	newText := currentText + "\n\n" + rulesText
	cm.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: newText}},
	})
}
