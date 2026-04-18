package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
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
func autopilotContinueInstruction(lastAssistantText string) string {
	return "Autopilot is enabled. Do not wait for user confirmation when a safe, reasonable next step is available. Choose the most reasonable assumption, state it briefly if helpful, and continue working until there is nothing meaningful left to do. If you only made partial progress, keep going instead of stopping for a progress update. If progress is blocked on a user action or external step that you cannot do yourself, use `ask_user` instead of repeating a blocked or waiting status.\n\nPrevious assistant message:\n" + lastAssistantText
}

// autopilotAskUserInstruction builds the injected user message that nudges
// the model to escalate an external blocker via ask_user.
func autopilotAskUserInstruction(lastAssistantText string) string {
	return "Autopilot is enabled. The previous assistant message indicates progress is blocked on a user action or external step. If you can perform that step yourself with the available tools, do it now. Otherwise, call the `ask_user` tool immediately with the specific action or information needed. Do not repeat a blocked or waiting summary.\n\nPrevious assistant message:\n" + lastAssistantText
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
	markers := []string{
		"next step", "next i", "next i'll", "still need", "still needs", "need to", "needs more",
		"follow up", "follow-up", "continue with", "continue by", "identified", "more to do",
		"another step", "hotspot", "todo", "then i can", "then i'll", "remaining work",
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
	markers := []string{
		"i inspected", "i checked", "i traced", "i investigated", "i analyzed", "i found",
		"i fixed", "i updated", "i changed", "i refactored", "i implemented", "i added",
		"identified", "root cause", "inspection shows",
		"我检查了", "我排查了", "我分析了", "我定位到", "我发现了", "我修复了", "我更新了", "我添加了",
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
