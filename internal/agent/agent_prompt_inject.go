package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// maybeInjectDynamicSystemPrompt builds the system prompt from scratch on
// every run, ensuring dynamic content and ratchet rules never accumulate
// across runs. The build order is:
//
//  1. Base system prompt (always present)
//  2. Dynamic system prompt from injector callback (if set)
//  3. Top learned ratchet rules (if any exist for this workspace)
//
// This function is called once at the start of each agent Run().
func (a *Agent) maybeInjectDynamicSystemPrompt() {
	a.mu.Lock()
	base := a.baseSystemPrompt
	fn := a.systemPromptInjector
	a.mu.Unlock()

	// Always start from base to prevent accumulation across runs.
	newText := base
	changed := false

	// Layer 2: dynamic system prompt from external injector.
	if fn != nil {
		extra := strings.TrimSpace(fn())
		if extra != "" {
			newText = base + "\n\n" + extra
			changed = true
		}
	}

	// Layer 3: proactive ratchet rules.
	rulesText := ""
	if workingDir := a.WorkingDir(); workingDir != "" {
		if rs := NewRuleStore(workingDir); rs != nil {
			rulesText = rs.TopRulesForPrompt(5)
		}
	}
	if rulesText != "" {
		newText = newText + "\n\n" + rulesText
		changed = true
		debug.Log("agent", "Injected learned ratchet rules into system prompt")
	}

	// Layer 4: playbook strategy hints (ACE-inspired).
	// Learn from past successful runs to guide future ones.
	playbookText := ""
	if workingDir := a.WorkingDir(); workingDir != "" {
		if pb := NewPlaybook(workingDir); pb != nil {
			playbookText = pb.HintsForPrompt(3)
		}
	}
	if playbookText != "" {
		newText = newText + "\n\n" + playbookText
		changed = true
		debug.Log("agent", "Injected playbook strategy hints into system prompt")
	}

	if !changed {
		return
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

// maybeInjectRatchetRules is a no-op retained for backward compatibility.
// Ratchet rule injection is now handled by maybeInjectDynamicSystemPrompt.
func (a *Agent) maybeInjectRatchetRules() {}
