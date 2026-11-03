package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (a *Agent) maybeInjectDynamicSystemPrompt(runPrompt string) {
	a.mu.Lock()
	base := a.baseSystemPrompt
	fn := a.systemPromptInjector
	a.mu.Unlock()

	defer a.logHarnessFingerprint()

	var dynamicParts []string

	if a.currentMode().IsCruiseMode() {
		if goal := a.getAutopilotGoal(); goal != "" {
			dynamicParts = append(dynamicParts, fmt.Sprintf(
				"⏵ ACTIVE GOAL (persistent — do not lose sight of this):\n%s\n\n"+
					"Continue working toward this goal. Do not ask the user for confirmation "+
					"unless genuinely blocked. Use best judgment for implementation decisions.",
				goal,
			))
		}
	}

	if fn != nil {
		extra := strings.TrimSpace(fn())
		if extra != "" {
			dynamicParts = append(dynamicParts, extra)
		}
	}

	for _, layer := range a.systemPromptLayers {
		if layer.fn == nil {
			continue
		}
		if extra := strings.TrimSpace(layer.fn()); extra != "" {
			dynamicParts = append(dynamicParts, extra)
		}
	}

	if rs := a.getRuleStore(); rs != nil {
		intent := classifyTaskType(runPrompt)
		rs.SetRunIntent(intent)
		rulesText := rs.TopRulesForPromptFiltered(5, intent)
		if rulesText != "" {
			dynamicParts = append(dynamicParts, rulesText)
			debug.Log("agent", "Injected learned ratchet rules into system prompt (intent=%s)", intent)
		}
	}

	if workingDir := a.WorkingDir(); workingDir != "" {
		if pb := NewPlaybook(workingDir); pb != nil {
			playbookText := pb.HintsForPrompt(runPrompt, 3)
			if playbookText != "" {
				dynamicParts = append(dynamicParts, playbookText)
				debug.Log("agent", "Injected playbook strategy hints into system prompt")
			}
		}

		if a.trajIntel != nil {
			if sec := a.trajIntel.RenderPromptSection(workingDir); sec != "" {
				dynamicParts = append(dynamicParts, sec)
				debug.Log("agent", "Injected past-run trajectory learnings into system prompt")
			}
		}

		if ft := FailureHintsForPrompt(workingDir, runPrompt, 2); ft != "" {
			dynamicParts = append(dynamicParts, ft)
			debug.Log("agent", "Injected failure attribution memory into system prompt")
		}
	}

	base = a.withTemporalContext(base)
	if base == "" && len(dynamicParts) == 0 {
		return
	}

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
