package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

const autopilotLoopGuardThreshold = 2

// currentMode returns the current permission mode from the policy.
func (a *Agent) currentMode() permission.PermissionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if policy, ok := a.policy.(modeAwarePolicy); ok {
		return policy.Mode()
	}
	return permission.SupervisedMode
}

// shouldAutopilotContinue returns true if the agent is in autopilot mode
// and the model's response text suggests there is more work to do.
func (a *Agent) shouldAutopilotContinue(text string) bool {
	if a.currentMode() != permission.AutopilotMode {
		return false
	}
	return shouldAutopilotKeepGoing(text)
}

// shouldAutopilotAskUser returns true if the agent is in autopilot mode
// and the model's response indicates an external blocker that requires
// user intervention via the ask_user tool.
func (a *Agent) shouldAutopilotAskUser(text string) bool {
	if a.currentMode() != permission.AutopilotMode {
		return false
	}
	if !looksLikeExternalBlocker(text) {
		return false
	}
	toolAny, ok := a.tools.Get("ask_user")
	if !ok {
		return false
	}
	if askTool, ok := toolAny.(interface{ HasHandler() bool }); ok {
		return askTool.HasHandler()
	}
	return false
}

// autopilotContinueInstruction builds the injected user message that nudges
// the model to keep working instead of waiting for confirmation.
// If a goal is set, it is included as an anchor to prevent scope drift.
func autopilotContinueInstruction(lastAssistantText, goal string) string {
	msg := "Autopilot: continue working on the original task. Do not stop for confirmation — pick the safest reasonable default and proceed. Do not start unrelated work. If you have completed all requested work, stop and summarize what was done."
	if goal != "" {
		msg += "\n\nYour autopilot goal: " + goal + "\nKeep your next steps focused on achieving this goal."
	}
	msg += "\n\nPrevious assistant message:\n" + lastAssistantText
	return msg
}

// autopilotAskUserInstruction builds the injected user message that nudges
// the model to escalate an external blocker via ask_user.
func autopilotAskUserInstruction(lastAssistantText, goal string) string {
	msg := "Autopilot: you reported a blocker. Either resolve it yourself with available tools, or call `ask_user` now with the specific question. Do not repeat the blocked status."
	if goal != "" {
		msg += "\n\nYour autopilot goal: " + goal
	}
	msg += "\n\nPrevious assistant message:\n" + lastAssistantText
	return msg
}

// autopilotGoalCheckInstruction asks the LLM to evaluate whether the goal
// has been achieved and either declare completion or continue working.
func autopilotGoalCheckInstruction(goal, lastAssistantText string) string {
	return "You are in autopilot mode. Evaluate your progress against the goal below.\n\n" +
		"Goal: " + goal + "\n\n" +
		"If the goal is fully achieved, end your response with exactly \"GOAL_COMPLETE\" on its own line, then provide a brief summary.\n" +
		"If the goal is NOT yet achieved, continue working immediately. Do not stop — call the appropriate tools to make progress.\n\n" +
		"Previous assistant message:\n" + lastAssistantText
}

// shouldAutopilotKeepGoing decides whether the model's text output suggests
// the agent should continue working autonomously.
func shouldAutopilotKeepGoing(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if looksLikeCompletionOrHandoff(trimmed) {
		return false
	}
	if looksLikeUserDecisionPrompt(trimmed) {
		return true
	}
	if looksLikeMoreWorkRemaining(trimmed) {
		return true
	}
	if looksLikeProgressUpdate(trimmed) {
		return true
	}
	return false
}

// shouldTriggerAutopilotLoopGuard returns true when the autopilot continuation
// streak is high enough and the text looks like a stalled loop.
func shouldTriggerAutopilotLoopGuard(text string, streak int) bool {
	if streak < autopilotLoopGuardThreshold {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	return looksLikeCompletionOrHandoff(trimmed) || looksLikeUserDecisionPrompt(trimmed) || looksLikeExternalBlocker(trimmed)
}

// --- Text classification heuristics ---
//
// These functions inspect the model's free-text output to classify its intent.
// They are used exclusively by the autopilot continuation logic above.

func looksLikeUserDecisionPrompt(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "?") || strings.Contains(trimmed, "？") {
		return true
	}
	markers := []string{
		"would you like", "should i", "which option", "which direction", "please provide",
		"please confirm", "can you confirm", "let me know", "tell me which", "what would you like",
		"what do you want", "how would you like", "do you want", "choose", "pick one",
		"请确认", "请提供", "请选择", "你希望", "是否", "要不要", "告诉我", "需要你", "先确认",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeCompletionOrHandoff(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	markers := []string{
		"all set", "wrapped up", "nothing else", "nothing meaningful left",
		"no further action", "that's it", "here's what i changed", "summary of changes",
		"completed the requested", "finished the requested", "completed the task", "finished the task",
		"completed the implementation", "finished the implementation", "completed the optimization pass",
		"finished the optimization pass",
		"nothing more to do", "no remaining work", "all changes are complete", "all changes complete",
		"all changes are in place", "done. no remaining work", "done. awaiting",
		"waiting for your next request", "ready for next task", "ready for the next task",
		"awaiting instructions", "no tasks pending", "no work to do", "standing by",
		"idle — no tasks pending", "idle - no tasks pending", "idle — no pending tasks",
		"idle - no pending tasks", "waiting for your next instruction",
		"let me know if you'd like", "if you'd like, i can", "if you want, i can",
		"feel free to ask", "feel free to tell me", "happy to help with anything else",
		"全部完成", "已经全部完成", "任务已完成", "这个任务已经完成", "优化已完成", "实现已完成",
		"所有任务已完成", "所有工作已完成", "工作已完成",
		"没有更多可做", "没有进一步需要处理", "如需我继续", "如果你希望我继续", "我还可以继续",
		"随时告诉我", "如果你还有其他", "如果你有其他", "还有其他任务需要我", "其他方面的具体任务需要我帮忙",
		"等待你的下一条指令", "等待你的下一步指令", "等待下一条指令", "等待下一步指令",
		"等待新指令", "等待新的指令", "等待后续指令", "待命中", "没有待处理任务", "没有任务待处理", "没有工作可做",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeMoreWorkRemaining(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "no remaining work") || strings.Contains(trimmed, "nothing more to do") {
		return false
	}
	// "todo" alone is too broad — it could be "todo_write" reference or a
	// mention of unrelated TODOs in code. Require action-oriented markers.
	markers := []string{
		"next step", "next i", "next i'll", "still need", "still needs", "need to", "needs more",
		"follow up", "follow-up", "continue with", "continue by", "more to do",
		"another step", "then i can", "then i'll", "remaining work",
		"接下来", "下一步", "还需要", "仍需", "还有", "后续", "继续", "再处理", "剩余",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeProgressUpdate(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	// Action verbs that indicate the model is actively working on something
	// and likely has more steps to complete. Exclude passive inspection-only
	// verbs ("I checked", "I looked at") that may indicate completion.
	markers := []string{
		"i fixed", "i updated", "i changed", "i refactored", "i implemented", "i added",
		"i removed", "i deleted", "i moved", "i renamed", "i created", "i replaced",
		"root cause", "inspection shows",
		"我修复了", "我更新了", "我添加了", "我删除了", "我移动了", "我重命名了",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeExternalBlocker(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	markers := []string{
		"blocked until", "blocked on user", "waiting for user to", "need user to",
		"awaiting restart", "awaiting gateway restart", "awaiting test results",
		"restart needed to validate", "needs to be restarted", "cannot proceed without",
		"can't proceed without", "need diagnostic logs", "share logs to continue",
		"需要用户", "等待用户", "阻塞于", "卡在", "需要重启", "等待重启", "等待测试结果", "需要日志",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
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
// it injects a meta-instruction asking the LLM to propose and confirm
// a goal with the user via ask_user.
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
			Text: "You are entering autopilot mode. Before starting work, you must define a clear, measurable Goal for this autonomous session.\n\n" +
				"Use the `ask_user` tool to confirm the goal with the user. Present the goal concisely — 1-3 sentences defining what 'done' looks like.\n" +
				"If the user's initial message already contains a clear, complete task description, you can use it as the goal and confirm with a brief ask_user.\n" +
				"After the user confirms the goal, proceed with the work immediately. Do not ask for further confirmation on individual steps.\n" +
				"When you believe the goal is achieved, end your response with \"GOAL_COMPLETE\" on its own line.",
		}},
	})
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

// isAutopilotGoalComplete checks if the LLM's text declares the goal
// complete via the GOAL_COMPLETE sentinel.
func (a *Agent) isAutopilotGoalComplete(text string) bool {
	if !a.hasAutopilotGoal() {
		return false
	}
	upper := strings.ToUpper(text)
	return strings.Contains(upper, "GOAL_COMPLETE")
}
