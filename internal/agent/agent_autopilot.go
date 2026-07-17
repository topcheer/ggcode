package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// currentMode returns the current permission mode from the policy.
func (a *Agent) currentMode() permission.PermissionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if policy, ok := a.policy.(modeAwarePolicy); ok {
		return policy.Mode()
	}
	return permission.SupervisedMode
}

// --- Autopilot Goal management ---

// clearGoalIfNotAutopilot clears the goal if the current mode is no longer
// autopilot. This handles TUI's cp.SetMode() which mutates the policy in
// place without calling agent.SetPermissionPolicy().
func (a *Agent) clearGoalIfNotAutopilot() {
	if a.currentMode() != permission.AutopilotMode {
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

// maybeInjectAutopilotGoalCollection is called at the start of each
// RunStreamWithContent. On the first call after entering autopilot mode,
// it injects a meta-instruction for goal handling.
//
// The instruction is designed so the LLM itself judges whether the user's
// prompt is clear enough to adopt directly as a goal, or whether it needs
// clarification via ask_user. No programmatic heuristic is applied.
//
// The LLM must declare the goal using a "GOAL:" sentinel line so the
// agent runtime can extract it for the strategist and GOAL_COMPLETE
// detection.
func (a *Agent) maybeInjectAutopilotGoalCollection() {
	if a.currentMode() != permission.AutopilotMode {
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

// extractGoalFromText checks if the LLM's output contains a "GOAL:" line
// and extracts the goal text. Returns the goal and true if found.
// Expected format: a line starting with "GOAL:" (case-insensitive), the
// rest of the line is the goal description.
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

// maybeSetAutopilotGoalFromLLMOutput scans the LLM's text output for a
// "GOAL:" declaration line and sets the autopilot goal if one is found
// and no goal has been set yet. This is called on every LLM response
// (both text-only and tool-call turns) to capture the goal as early as
// possible.
func (a *Agent) maybeSetAutopilotGoalFromLLMOutput(text string) {
	if a.currentMode() != permission.AutopilotMode {
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

// SetAutopilotGoal stores the confirmed goal text. Called by the
// ask_user result handler when the goal confirmation question is answered.
func (a *Agent) SetAutopilotGoal(goal string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autopilotGoal = goal
	a.autopilotGoalSet = true
	debug.Log("agent", "autopilot goal set: %s", goal)
}

// getAutopilotGoal returns the current goal text (empty if none).
func (a *Agent) getAutopilotGoal() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autopilotGoal
}

// hasAutopilotGoal returns true if a goal has been confirmed.
func (a *Agent) hasAutopilotGoal() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autopilotGoalSet && a.autopilotGoal != ""
}

// clearAutopilotGoal removes the current goal.
func (a *Agent) clearAutopilotGoal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autopilotGoal = ""
	a.autopilotGoalSet = false
	debug.Log("agent", "autopilot goal cleared")
}
