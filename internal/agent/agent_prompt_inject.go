package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
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

	// Stamp the harness configuration (system prompt + check registry + tool
	// set) into the debug ring. Deferred so it captures the prompt rebuilt by
	// this function on every return path; logs only on change (first run or
	// scaffolding mutation). See harness_fingerprint.go for rationale.
	defer a.logHarnessFingerprint()

	// Collect dynamic layers.
	var dynamicParts []string

	// Layer 1.5: autopilot goal (must survive compaction).
	// The goal is injected into the system prompt rather than the conversation
	// body so it persists across context compaction. Without this, the main
	// agent loses sight of its objective after summarization — only the
	// strategist remembers it. This is the "Goal Mode" pattern (Codex CLI /goal):
	// a persistent directive that survives compaction and interruptions.
	if a.currentMode() == permission.AutopilotMode {
		if goal := a.getAutopilotGoal(); goal != "" {
			dynamicParts = append(dynamicParts, fmt.Sprintf(
				"⏵ ACTIVE GOAL (persistent — do not lose sight of this):\n%s\n\n"+
					"Continue working toward this goal. Do not ask the user for confirmation "+
					"unless genuinely blocked. Use best judgment for implementation decisions.",
				goal,
			))
		}
	}

	// Layer 2: dynamic system prompt from external injector.
	if fn != nil {
		extra := strings.TrimSpace(fn())
		if extra != "" {
			dynamicParts = append(dynamicParts, extra)
		}
	}

	// Layer 2.5: named prompt layers (e.g. resume reconciliation). Same
	// dynamic bucket as layer 2 — rebuilt every run, never cached.
	for _, layer := range a.systemPromptLayers {
		if layer.fn == nil {
			continue
		}
		if extra := strings.TrimSpace(layer.fn()); extra != "" {
			dynamicParts = append(dynamicParts, extra)
		}
	}

	// Layer 3: proactive ratchet rules, selected for task relevance
	// (token-efficient retrieval: Mem0 "State of AI Agent Memory" 2026;
	// arXiv:2603.07670 read-path optimization). The most recent user text
	// classifies the task; irrelevant rule categories are dropped while a
	// small global floor keeps the highest-value lessons visible.
	if workingDir := a.WorkingDir(); workingDir != "" {
		if rs := NewRuleStore(workingDir); rs != nil {
			var lastUser string
			if cm, ok := a.contextManager.(*context.Manager); ok {
				lastUser = lastUserPromptText(cm.Messages())
			}
			rulesText := rs.TopRulesForTask(5, lastUser)
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
	// Anchor temporal context to session start and fold it into the cacheable
	// base layer (Temporal Context Injection baseline practice).
	base = a.withTemporalContext(base)
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

	// System prompt budget check: warn if the prompt is excessively large.
	// A system prompt consuming >15% of the context window significantly
	// reduces available conversation space and accelerates compaction.
	ctxWindow := a.contextManager.ContextWindow()
	if ctxWindow > 0 {
		estTokens := context.EstimateTokens(fullText)
		ratio := float64(estTokens) / float64(ctxWindow)
		if ratio > 0.15 {
			debug.Log("agent", "WARNING: system prompt is %.1f%% of context window (%d tokens / %d). Consider trimming memory files, ratchet rules, or reducing ExtraPrompt.", ratio*100, estTokens, ctxWindow)
		}
	}

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

// temporalContextLine renders the session-anchored temporal header. The
// timestamp is anchored to the agent's session start (lazily captured on
// first call) rather than sampled on every invocation, so the rendered
// bytes stay identical across all iterations of a run. This preserves
// KV-cache prefix reuse within the session (see #2445 prefix-stability
// findings); sessions that outlive the header's freshness are expected to
// call the current_time tool for a live reading instead.
func (a *Agent) temporalContextLine() string {
	a.mu.Lock()
	if a.temporalAnchor.IsZero() {
		a.temporalAnchor = time.Now()
	}
	now := a.temporalAnchor
	a.mu.Unlock()

	name, _ := now.Zone()
	utcOffset := now.Format("-07:00")
	return fmt.Sprintf("Current date/time: %s (%s), %s %s (UTC offset %s). "+
		"Treat this as the reference point for all time-sensitive reasoning; "+
		"call the current_time tool if the actual wall-clock time matters and may have drifted.",
		now.Format("2006-01-02"), now.Format("Monday"), now.Format("15:04"), name, utcOffset)
}

// withTemporalContext prepends the temporal header to a non-empty base
// system prompt. Placement at the very start of the system prompt follows
// temporal-grounding research showing date-sensitive reasoning is most
// reliable when the timestamp leads the prompt. Empty bases stay empty so
// setups that rely on the absence of a system message are undisturbed.
func (a *Agent) withTemporalContext(base string) string {
	if strings.TrimSpace(base) == "" {
		return base
	}
	return a.temporalContextLine() + "\n\n" + base
}

// maybeInjectRatchetRules is a no-op retained for backward compatibility.
// Ratchet rule injection is now handled by maybeInjectDynamicSystemPrompt.
func (a *Agent) maybeInjectRatchetRules() {}

// maxClassifyPromptRunes caps how much of the latest user prompt is fed
// into task classification; the head is sufficient for keyword matching.
const maxClassifyPromptRunes = 2048

// lastUserPromptText returns the text of the most recent user message for
// task classification. Only pure text blocks count: tool results are also
// delivered in user-role messages, but as tool_result blocks without Text.
// Messages without text are skipped so an earlier real user prompt is still
// found. Returns "" when no user text exists (e.g. before the first turn),
// which makes rule injection fall back to the unfiltered global top-N.
func lastUserPromptText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		var b strings.Builder
		for _, blk := range msgs[i].Content {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
				b.WriteByte(' ')
			}
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}
		// Classification only needs the head of the prompt; cap rune-safely.
		if runes := []rune(text); len(runes) > maxClassifyPromptRunes {
			text = string(runes[:maxClassifyPromptRunes])
		}
		return text
	}
	return ""
}
