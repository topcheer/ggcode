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
//  1. Base system prompt (always present, marked cacheable)
//  2. Dynamic system prompt from injector callback (if set)
//  3. Top learned ratchet rules (if any exist for this workspace)
//  4. Playbook strategy hints (if any exist for this workspace)
//
// The static base (layer 1) is emitted as a separate content block with
// Cache=true so providers like Anthropic can cache it across turns even
// when the dynamic layers (2-4) change between runs. This follows the
// "Don't Break the Cache" finding (arXiv:2601.06007): placing stable
// content at the start with its own cache breakpoint maximises KV cache
// reuse, saving 40-80% of system prompt token costs.
//
// This function is called once at the start of each agent Run().
func (a *Agent) maybeInjectDynamicSystemPrompt() {
	a.mu.Lock()
	base := a.baseSystemPrompt
	fn := a.systemPromptInjector
	a.mu.Unlock()

	// Collect dynamic layers.
	var dynamicParts []string

	// Layer 2: dynamic system prompt from external injector.
	if fn != nil {
		extra := strings.TrimSpace(fn())
		if extra != "" {
			dynamicParts = append(dynamicParts, extra)
		}
	}

	// Layer 3: proactive ratchet rules.
	if workingDir := a.WorkingDir(); workingDir != "" {
		if rs := NewRuleStore(workingDir); rs != nil {
			rulesText := rs.TopRulesForPrompt(5)
			if rulesText != "" {
				dynamicParts = append(dynamicParts, rulesText)
				debug.Log("agent", "Injected learned ratchet rules into system prompt")
			}
		}
	}

	// Layer 4: playbook strategy hints (ACE-inspired).
	if workingDir := a.WorkingDir(); workingDir != "" {
		if pb := NewPlaybook(workingDir); pb != nil {
			playbookText := pb.HintsForPrompt(3)
			if playbookText != "" {
				dynamicParts = append(dynamicParts, playbookText)
				debug.Log("agent", "Injected playbook strategy hints into system prompt")
			}
		}
	}

	// Skip entirely when there is no system prompt and no dynamic content.
	// This preserves backward compatibility: tests and setups that rely on
	// the absence of a system message are not disturbed.
	base = strings.TrimSpace(base)
	if base == "" && len(dynamicParts) == 0 {
		return
	}

	// Build the full prompt text to check if it changed since last injection.
	// This avoids redundant countTokens + UpdateFirstSystemMessage calls on
	// every agent iteration when the prompt content is identical.
	var fullText string
	if len(dynamicParts) == 0 {
		fullText = base
	} else {
		fullText = base + "\n\n" + strings.Join(dynamicParts, "\n\n")
	}
	if fullText == a.lastInjectedSystemPrompt {
		return
	}
	a.lastInjectedSystemPrompt = fullText

	cm, ok := a.contextManager.(*context.Manager)
	if !ok {
		return
	}

	// Build content blocks: static base (cacheable) + dynamic (not cached).
	// When there is no dynamic content, emit a single cached block.
	if len(dynamicParts) == 0 {
		cm.UpdateFirstSystemMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: base, Cache: true}},
		})
		return
	}

	dynamicText := strings.Join(dynamicParts, "\n\n")
	cm.UpdateFirstSystemMessage(provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: base, Cache: true},
			{Type: "text", Text: dynamicText},
		},
	})
}

// maybeInjectRatchetRules is a no-op retained for backward compatibility.
// Ratchet rule injection is now handled by maybeInjectDynamicSystemPrompt.
func (a *Agent) maybeInjectRatchetRules() {}
