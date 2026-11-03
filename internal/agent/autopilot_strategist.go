package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const maxAutopilotStrategistCalls = 100

const strategistCompleteMarker = "<GOAL_ACHIEVED/>"

func stripStrategistCompleteMarker(s string) string {
	if idx := indexFoldASCII(s, strategistCompleteMarker); idx >= 0 {
		return strings.TrimSpace(s[idx+len(strategistCompleteMarker):])
	}
	return s
}

const maxConsecutiveStrategistNoProgress = 3

type strategistResult struct {
	Guidance string
	Complete bool
}

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

If ANY of these conditions cannot be confirmed from the conversation evidence (tool results you can see), do NOT declare the goal achieved. Instead, direct the agent to verify the missing item. For example: "Run the test suite to confirm all tests pass" or "Build the project and fix any compilation errors."

IMPORTANT — Completion signal format:
- The ONLY signal that stops the agent is the exact token <GOAL_ACHIEVED/> (angle brackets, trailing slash) placed at the START of your response, followed by the summary on the next line.
- If the goal is NOT yet achieved and you want the agent to CONTINUE working, you MUST NOT emit <GOAL_ACHIEVED/> — even to negate it. Do not write "don't emit <GOAL_ACHIEVED/>", do not quote it, do not mention it. Just give the next instruction plainly. Any appearance of <GOAL_ACHIEVED/> in your response is treated as a stop signal.
- Remember: false completion wastes the user's time because they must manually check and redo incomplete work. When in doubt, continue — not stop.

If and only if ALL the above conditions are met, start your response with <GOAL_ACHIEVED/> on the first line and provide a brief summary on the following line.

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

	resp, err := a.auxProviderFor().Chat(ctx, messages, nil)
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

	if strings.Contains(upper, strategistCompleteMarker) {
		result.Complete = true
	}

	debug.Log("agent", "autopilot strategist: complete=%t guidance_len=%d", result.Complete, len(result.Guidance))
	return result, nil
}

func (a *Agent) extractStrategistContext() string {
	msgs := a.contextManager.Messages()

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

				summary := summarizeToolUse(block.ToolName, block.Input)
				parts = append(parts, summary)
			case "tool_result":

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

func summarizeToolUse(toolName string, input json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return fmt.Sprintf("[Tool Call: %s]", toolName)
	}

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

func summarizeToolResult(toolName, output string, isError bool) string {
	if output == "" {
		return ""
	}
	tag := "OK"
	if isError {
		tag = "ERROR"
	}
	runes := []rune(output)

	if toolName == "run_command" || toolName == "bash" || toolName == "start_command" || toolName == "powershell" || toolName == "read_command_output" || toolName == "wait_command" {
		const headLen = 300
		const tailLen = 700
		if len(runes) <= headLen+tailLen {
			return fmt.Sprintf("[Tool Result %s: %s]", tag, string(runes))
		}
		head := string(runes[:headLen])
		tail := string(runes[len(runes)-tailLen:])
		return fmt.Sprintf("[Tool Result %s: %s\n...[%d chars omitted]...\n%s]", tag, head, len(runes)-headLen-tailLen, tail)
	}

	const maxOut = 500
	if len(runes) > maxOut {
		return fmt.Sprintf("[Tool Result %s: %s...]", tag, string(runes[:maxOut]))
	}
	return fmt.Sprintf("[Tool Result %s: %s]", tag, string(runes))
}
