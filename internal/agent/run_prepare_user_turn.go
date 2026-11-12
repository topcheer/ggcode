package agent

import (
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The user-turn preparation block (unpaired
// tool-call reconciliation, goal/correction/sentiment feedback injection,
// ambiguity advisory, perf regression / dynamic system prompt / ratchet
// rules, usage hints attach, and tool-definition/tool-search setup)
// becomes an explicit method; behavior unchanged (verbatim order).

// prepareUserTurn runs the pre-loop preparation keyed to this user turn
// (verbatim order).
func (a *Agent) prepareUserTurn(userPromptForStats string) (toolDefs []provider.ToolDefinition, transientCompactWarned bool) {
	if a.ReconcileToolCalls() {
		debug.Log("agent", "RunStreamWithContent: reconciled unpaired tool_calls")
	}

	a.clearGoalIfNotAutopilot()
	a.maybeInjectAutopilotGoalCollection()
	a.maybeInjectCorrectionFeedback()
	a.maybeInjectSentimentFeedback(userPromptForStats)

	if ambMsg := a.checkAmbiguityPoints(userPromptForStats); ambMsg != "" {
		debug.Log("agent", "ambiguity point detector: injecting disambiguation guidance")
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: ambMsg,
			}},
		})
	}
	a.maybeInjectPerfRegression()
	a.maybeInjectDynamicSystemPrompt(userPromptForStats)
	a.maybeInjectRatchetRules()

	a.mu.RLock()
	uhDir := a.workingDir
	a.mu.RUnlock()
	if uhDir != "" {
		a.usageHints.attach(filepath.Join(uhDir, ".ggcode"))
		a.tools.SetDescriptionAugmenter(a.usageHints.OverlayFor)
	}
	toolDefs = a.tools.ToDefinitions()
	a.toolSearch.init(toolDefs)

	if enabler, ok := a.provider.(interface{ ServerToolSearchActive() bool }); ok && enabler.ServerToolSearchActive() {
		a.toolSearch.disable()
		a.serverToolSearch = true
		debug.Log("agent", "server-side tool search active: MCP schemas deferred via defer_loading")
	}
	if a.toolSearch.enabled {
		debug.Log("agent", "tool search: %d MCP tool schemas deferred behind %s", len(a.toolSearch.deferred), ToolSearchToolName)
	}
	if cm, ok := a.contextManager.(interface{ SetToolDefinitionOverhead(int) }); ok {
		cm.SetToolDefinitionOverhead(estimateToolDefinitionOverhead(toolDefs))
	}
	return toolDefs, transientCompactWarned
}
