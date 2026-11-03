package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

func (a *Agent) currentMode() permission.PermissionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if policy, ok := a.policy.(modeAwarePolicy); ok {
		return policy.Mode()
	}
	return permission.SupervisedMode
}

func (a *Agent) IsAutopilotMode() bool {
	if a == nil {
		return false
	}
	return a.currentMode().IsCruiseMode()
}

func (a *Agent) clearGoalIfNotAutopilot() {
	if !a.currentMode().IsCruiseMode() {
		a.mu.Lock()
		if a.autopilotGoal != "" || a.autopilotGoalSet || a.autopilotGoalAsked {
			a.autopilotGoal = ""
			a.autopilotGoalSet = false
			a.autopilotGoalAsked = false
			debug.Log("agent", "autopilot goal cleared (mode no longer autopilot)")
		}
		a.mu.Unlock()
	}
}

func (a *Agent) SuppressAutopilotInjection() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.suppressAutopilotInjection = true
	a.mu.Unlock()
}

func (a *Agent) maybeInjectAutopilotGoalCollection() {
	if a.suppressAutopilotInjection {
		return
	}
	if !a.currentMode().IsCruiseMode() {
		return
	}
	a.mu.Lock()
	if a.autopilotGoalAsked {
		a.mu.Unlock()
		return
	}
	a.autopilotGoalAsked = true
	a.mu.Unlock()

	debug.Log("agent", "autopilot: injecting goal collection instruction")
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "You are entering autopilot mode.\n\n" +
				"Review the user's message above. If it already describes a clear, specific task that you can work on autonomously, adopt it as your goal and start working immediately. Do NOT ask for confirmation — the user chose autopilot mode because they want autonomous execution.\n\n" +
				"Only use `ask_user` if the user's intent is genuinely ambiguous and you cannot reasonably infer what they want. In that case, ask a single concise question to clarify the goal.\n\n" +
				"**Declaring your goal (required):** Before starting work, output a line in this exact format:\n" +
				"```\nGOAL: <one-sentence description of what you will achieve>\n```\n" +
				"This goal line will be parsed by the runtime to track progress. Place it at the top of your response, before any work begins.\n\n" +
				"Once the goal is set, work toward it fully autonomously. Do not pause for step-by-step approval.",
		}},
	})
}

func extractGoalFromText(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 5 {
			upper := strings.ToUpper(trimmed[:5])
			if upper == "GOAL:" {
				goal := strings.TrimSpace(trimmed[5:])
				if goal != "" {
					return goal, true
				}
			}
		}
	}
	return "", false
}

func (a *Agent) maybeSetAutopilotGoalFromLLMOutput(text string) {

	if !a.currentMode().IsCruiseMode() {
		return
	}
	a.mu.Lock()
	if a.autopilotGoalSet {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	goal, found := extractGoalFromText(text)
	if !found {
		return
	}

	a.mu.Lock()
	a.autopilotGoal = goal
	a.autopilotGoalSet = true
	a.mu.Unlock()
	debug.Log("agent", "autopilot goal extracted from LLM output: %s", goal)
}

func (a *Agent) SetAutopilotGoal(goal string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autopilotGoal = goal
	a.autopilotGoalSet = true
	debug.Log("agent", "autopilot goal set: %s", goal)
}

func (a *Agent) getAutopilotGoal() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autopilotGoal
}

func (a *Agent) GetAutopilotGoal() string {
	return a.getAutopilotGoal()
}

func (a *Agent) hasAutopilotGoal() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autopilotGoalSet && a.autopilotGoal != ""
}

func (a *Agent) ClearAutopilotGoal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autopilotGoal = ""
	a.autopilotGoalSet = false
	debug.Log("agent", "autopilot goal cleared")
}
