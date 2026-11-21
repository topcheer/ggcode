package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// volatileTurnContextHeader delimits the per-prompt dynamic context block
// (#5807) that rides inside the new user turn as a trailing text block, so
// the model can tell agent-assembled context apart from user prose.
const volatileTurnContextHeader = "[Turn context - agent-assembled for this prompt]"

// maybeInjectStableSystemHead assembles the STABLE system head (messages[0]).
//
// #5807: the head only carries session-stable segments - base prompt,
// temporal anchor, cruise-mode goal, keyed instruction blocks (#5698) and
// trajectory learnings - so a new user prompt never rewrites it and the
// provider prefix cache survives across turns. Per-prompt segments
// (intent-filtered ratchet rules, playbook hints, failure attribution
// memory, injector/layer output) render into the new user turn instead:
// see renderVolatileTurnContext.
func (a *Agent) maybeInjectStableSystemHead() {
	defer a.logHarnessFingerprint()

	a.mu.Lock()
	base := a.baseSystemPrompt
	stableSections := append([]string(nil), a.stableSystemSections...)
	a.mu.Unlock()

	var headParts []string
	for _, s := range stableSections {
		headParts = append(headParts, s)
	}

	if a.currentMode().IsCruiseMode() {
		if goal := a.getAutopilotGoal(); goal != "" {
			headParts = append(headParts, fmt.Sprintf(
				"⏵ ACTIVE GOAL (persistent — do not lose sight of this):\n%s\n\n"+
					"Continue working toward this goal. Do not ask the user for confirmation "+
					"unless genuinely blocked. Use best judgment for implementation decisions.",
				goal,
			))
		}
	}

	// #5698: durable keyed session instructions render as stable keyed
	// blocks (sorted by key). Only a changed entry's block rewrites, so the
	// prefix cache stays valid for untouched blocks. announced carries the
	// keys whose one-line delta rode this assembly.
	var keyedAnnounced []string
	if keyedBlock, announced := a.renderKeyedInstructionBlocks(); keyedBlock != "" {
		headParts = append(headParts, keyedBlock)
		keyedAnnounced = announced
		debug.Log("agent", "Injected keyed session instructions into system prompt (%d announce line(s))", len(announced))
	}

	if workingDir := a.WorkingDir(); workingDir != "" && a.trajIntel != nil {
		if sec := a.trajIntel.RenderPromptSection(workingDir); sec != "" {
			headParts = append(headParts, sec)
			debug.Log("agent", "Injected past-run trajectory learnings into system prompt")
		}
	}

	base = a.withTemporalContext(base)
	if base == "" && len(headParts) == 0 {
		return
	}

	var fullText string
	switch {
	case len(headParts) == 0:
		fullText = base
	case base == "":
		fullText = strings.Join(headParts, "\n\n")
	default:
		fullText = base + "\n\n" + strings.Join(headParts, "\n\n")
	}
	if fullText == a.lastInjectedSystemPrompt {
		return
	}
	a.lastInjectedSystemPrompt = fullText

	// #5698: the announce line(s) rode this shipped assembly — consume the
	// announce bits now (step-boundary emission, exactly once per change).
	if len(keyedAnnounced) > 0 {
		a.consumeKeyedInstructionAnnounce(keyedAnnounced)
		debug.Log("session-instructions", "announce emitted at step boundary for %d key(s)", len(keyedAnnounced))
	}

	ctxWindow := a.contextManager.ContextWindow()
	headTokens := context.EstimateTokens(fullText)
	if ctxWindow > 0 {
		ratio := float64(headTokens) / float64(ctxWindow)
		if ratio > 0.15 {
			debug.Log("agent", "WARNING: system prompt is %.1f%% of context window (%d tokens / %d). Consider trimming memory files, ratchet rules, or reducing ExtraPrompt.", ratio*100, headTokens, ctxWindow)
		}
	}

	cm, ok := a.contextManager.(*context.Manager)
	if !ok {
		return
	}

	debug.Log("prompt-cache", "stable system head assembled: headTokens=%d window=%d", headTokens, ctxWindow)

	// Single stable block carrying the cache breakpoint at the stable
	// boundary (#5807): everything before it is session-stable, everything
	// per-prompt rides the user turn tail instead.
	cm.UpdateFirstSystemMessage(provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: fullText, Cache: true},
		},
	})
}

// renderVolatileTurnContext renders the per-prompt dynamic segments that used
// to rewrite the system head on every user turn (#5807): injector/layer
// output, intent-filtered ratchet rules, playbook strategy hints and failure
// attribution memory. The caller attaches the result to the NEW user turn as
// a trailing text block: the turn is an append, so the old prefix stays
// byte-identical and only the tail block pays the cache write cost.
func (a *Agent) renderVolatileTurnContext(runPrompt string) string {
	a.mu.RLock()
	fn := a.systemPromptInjector
	layers := append([]systemPromptLayer(nil), a.systemPromptLayers...)
	a.mu.RUnlock()

	var parts []string

	if fn != nil {
		if extra := strings.TrimSpace(fn()); extra != "" {
			parts = append(parts, extra)
		}
	}

	for _, layer := range layers {
		if layer.fn == nil {
			continue
		}
		if extra := strings.TrimSpace(layer.fn()); extra != "" {
			parts = append(parts, extra)
		}
	}

	if rs := a.getRuleStore(); rs != nil {
		intent := classifyTaskType(runPrompt)
		rs.SetRunIntent(intent)
		rulesText := rs.TopRulesForPromptFiltered(5, intent)
		if rulesText != "" {
			parts = append(parts, rulesText)
			debug.Log("agent", "Injected learned ratchet rules into turn context (intent=%s)", intent)
		}
	}

	if workingDir := a.WorkingDir(); workingDir != "" {
		if pb := NewPlaybook(workingDir); pb != nil {
			playbookText := pb.HintsForPrompt(runPrompt, 3)
			if playbookText != "" {
				parts = append(parts, playbookText)
				debug.Log("agent", "Injected playbook strategy hints into turn context")
			}
		}

		if ft := FailureHintsForPrompt(workingDir, runPrompt, 2); ft != "" {
			parts = append(parts, ft)
			debug.Log("agent", "Injected failure attribution memory into turn context")
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return volatileTurnContextHeader + "\n\n" + strings.Join(parts, "\n\n")
}

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

func (a *Agent) withTemporalContext(base string) string {
	if strings.TrimSpace(base) == "" {
		return base
	}
	return a.temporalContextLine() + "\n\n" + base
}

func (a *Agent) maybeInjectRatchetRules() {}
