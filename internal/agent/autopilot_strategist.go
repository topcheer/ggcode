package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// maxAutopilotStrategistCalls is the per-Run budget for strategist LLM calls.
// See agent.go for rationale.
const maxAutopilotStrategistCalls = 30

// strategistResult is the output from the autopilot strategist LLM call.
type strategistResult struct {
	Guidance string // text to inject into the main agent as a user message
	Complete bool   // true if the strategist believes the goal is achieved
}

// runAutopilotStrategist calls an independent LLM reasoning pass to decide
// what the main agent should do next. It receives only the conversation
// context (compaction summary + recent messages with all tool_use/tool_result
// stripped, keeping only text). The result is injected as a user message to
// drive the next turn.
//
// This replaces the old deterministic text-pattern-matching autopilot logic
// (shouldAutopilotContinue, shouldAutopilotAskUser, looksLike*, etc.) with a
// single LLM-based reasoning step that understands context holistically.
func (a *Agent) runAutopilotStrategist(ctx context.Context, lastAssistantText string) (*strategistResult, error) {
	goal := a.getAutopilotGoal()
	contextStr := a.extractStrategistContext()

	systemPrompt := `You are an autonomous task strategist embedded in a coding agent running in autopilot mode.

Your role is to analyze the current conversation state and decide what the agent should do next. You have full visibility into the conversation history (summary of earlier work + recent exchanges).

The agent operates in three natural phases:
1. RESEARCH — understanding the codebase architecture, studying competitors, exploring latest trends, approaches, and relevant papers. Research is open-ended and iterative.
2. PLAN — converting research findings into concrete, ordered implementation steps with clear acceptance criteria.
3. IMPLEMENT — executing the plan step by step, verifying each change builds/passes tests, and iterating when issues arise.

Based on the conversation so far, your job is to decide what happens next:
- If research is incomplete, superficial, or missing an important angle (architecture, competitors, recent papers, best practices), direct the agent to research that specific area. Name concrete files to read, patterns to look for, or topics to web-search.
- If research is sufficient but no concrete plan exists, direct the agent to produce a structured implementation plan with numbered steps. Each step should be independently verifiable.
- If a plan exists but implementation is incomplete, direct the agent to continue with the specific next step. Reference which step number and what it entails.
- If the agent is stuck in a loop or making no progress, suggest a different approach — try a different file, a different technique, or step back and reconsider.
- If the agent asks for user confirmation or permission to proceed, tell it to use best judgment and continue autonomously — autopilot mode means minimal user interaction.

CRITICAL — Anti-premature-convergence rules:
Before declaring GOAL_ACHIEVED, you MUST verify ALL of the following:
1. Every file mentioned in the plan was created/modified AND the last tool result for each was successful (not ERROR).
2. Build/compile was run and succeeded — you saw a successful build/test tool result, not just the agent's claim.
3. Tests were run and passed — you saw test output with PASS/OK, not just "tests should pass".
4. No pending TODO items, no commented-out code blocks, no "will do this next" placeholders in the output.
5. The goal's acceptance criteria are ALL met — if the goal was "add feature X with tests", both the feature AND tests must be verified.

If ANY of these conditions cannot be confirmed from the conversation evidence (tool results you can see), do NOT declare GOAL_ACHIEVED. Instead, direct the agent to verify the missing item. For example: "Run the test suite to confirm all tests pass" or "Build the project and fix any compilation errors."

Remember: false GOAL_ACHIEVED wastes the user's time because they must manually check and redo incomplete work. When in doubt, continue — not stop.

If and only if ALL the above conditions are met, start your response with "GOAL_ACHIEVED" and provide a brief summary.

Be specific and actionable. Reference concrete findings from the conversation. Do not repeat generic advice. Do not hedge — give a clear, confident direction.
Your response will be injected directly as a user message into the agent's next turn, so write it as a direct instruction to the agent.`

	userPrompt := fmt.Sprintf(`## Autopilot Goal
%s

## Conversation Context
%s

## Agent's Last Output
%s

## Budget
This is strategist call %d of %d for this run. Plan your guidance accordingly — if the goal is close to completion, prioritize verification and cleanup over new exploration.

What should the agent do next?`, goal, contextStr, lastAssistantText, a.autopilotStrategistCount, maxAutopilotStrategistCalls)

	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userPrompt}}},
	}

	resp, err := a.provider.Chat(ctx, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("strategist call failed: %w", err)
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		a.emitUsageWithSource(resp.Usage, "strategist")
	}

	var guidance strings.Builder
	for _, block := range resp.Message.Content {
		if block.Type == "text" {
			guidance.WriteString(block.Text)
		}
	}

	result := &strategistResult{
		Guidance: strings.TrimSpace(guidance.String()),
	}

	upper := strings.ToUpper(result.Guidance)
	if strings.HasPrefix(upper, "GOAL_ACHIEVED") {
		result.Complete = true
	}

	debug.Log("agent", "autopilot strategist: complete=%t guidance_len=%d", result.Complete, len(result.Guidance))
	return result, nil
}

// extractStrategistContext builds a condensed view of the conversation for
// the strategist. It finds the compaction summary (if any) and then includes
// all subsequent messages, converting tool_use/tool_result blocks into short
// summaries so the strategist can see what was done and what happened.
//
// Without tool results, the strategist cannot judge whether a goal is actually
// achieved — it would only see the assistant's claims ("tests pass now") with
// no evidence.
//
// If no compaction summary exists yet, it falls back to the last 20 messages.
func (a *Agent) extractStrategistContext() string {
	msgs := a.contextManager.Messages()

	// Find the compaction summary boundary.
	summaryIdx := -1
	for i, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type == "text" && strings.Contains(block.Text, "[Previous conversation summary]") {
				summaryIdx = i
				break
			}
		}
		if summaryIdx >= 0 {
			break
		}
	}

	var b strings.Builder

	if summaryIdx >= 0 {
		// Include the compaction summary itself.
		for _, block := range msgs[summaryIdx].Content {
			if block.Type == "text" {
				b.WriteString("--- Earlier Conversation Summary ---\n")
				b.WriteString(block.Text)
				b.WriteString("\n\n--- Recent Conversation ---\n")
				break
			}
		}
		msgs = msgs[summaryIdx+1:]
	} else {
		// No compaction yet — take last 20 messages.
		start := len(msgs) - 20
		if start < 0 {
			start = 0
		}
		msgs = msgs[start:]
		b.WriteString("--- Conversation ---\n")
	}

	for _, msg := range msgs {
		var parts []string
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				t := strings.TrimSpace(block.Text)
				if t != "" {
					parts = append(parts, t)
				}
			case "tool_use":
				// Summarize what tool was called with what key params.
				summary := summarizeToolUse(block.ToolName, block.Input)
				parts = append(parts, summary)
			case "tool_result":
				// Include a truncated view of the tool output.
				summary := summarizeToolResult(block.ToolName, block.Output, block.IsError)
				if summary != "" {
					parts = append(parts, summary)
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		text := strings.Join(parts, "\n")
		// Truncate very long individual messages to keep the strategist input manageable.
		const maxMsgLen = 3000
		if len(text) > maxMsgLen {
			runes := []rune(text)
			if len(runes) > maxMsgLen {
				text = string(runes[:maxMsgLen]) + "... [truncated]"
			}
		}

		switch msg.Role {
		case "user":
			b.WriteString("[User] ")
			b.WriteString(text)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("[Assistant] ")
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}

	return strings.TrimSpace(b.String())
}

// summarizeToolUse produces a one-line summary of a tool call for the
// strategist context. It shows the tool name and key parameters.
func summarizeToolUse(toolName string, input json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return fmt.Sprintf("[Tool Call: %s]", toolName)
	}
	// Extract the most relevant parameter (path, command, pattern, etc).
	keys := []string{"path", "file_path", "command", "pattern", "url", "query", "name", "agent", "task"}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 200 {
				s = s[:200] + "..."
			}
			return fmt.Sprintf("[Tool Call: %s(%s=%s)]", toolName, key, s)
		}
	}
	return fmt.Sprintf("[Tool Call: %s]", toolName)
}

// summarizeToolResult produces a truncated summary of a tool result for the
// strategist context. It includes enough of the output to evidence success or
// failure without consuming excessive tokens.
//
// For command/shell tools, the result is split: first 300 chars (command echo,
// initial output) + last 700 chars (test results, PASS/FAIL, error summary).
// This is critical because build/test output puts the success/failure verdict
// at the END — a head-only truncation would hide the exact evidence the
// strategist's anti-premature-convergence gate needs.
func summarizeToolResult(toolName, output string, isError bool) string {
	if output == "" {
		return ""
	}
	tag := "OK"
	if isError {
		tag = "ERROR"
	}
	runes := []rune(output)

	// Command tools need head+tail to capture test verdicts at the end.
	if toolName == "run_command" || toolName == "bash" || toolName == "start_command" || toolName == "powershell" {
		const headLen = 300
		const tailLen = 700
		if len(runes) <= headLen+tailLen {
			return fmt.Sprintf("[Tool Result %s: %s]", tag, string(runes))
		}
		head := string(runes[:headLen])
		tail := string(runes[len(runes)-tailLen:])
		return fmt.Sprintf("[Tool Result %s: %s\n...[%d chars omitted]...\n%s]", tag, head, len(runes)-headLen-tailLen, tail)
	}

	// Other tools: head-only truncation at 500 chars (file content headers,
	// search match previews, etc. — the relevant info is at the top).
	const maxOut = 500
	if len(runes) > maxOut {
		return fmt.Sprintf("[Tool Result %s: %s...]", tag, string(runes[:maxOut]))
	}
	return fmt.Sprintf("[Tool Result %s: %s]", tag, string(runes))
}
