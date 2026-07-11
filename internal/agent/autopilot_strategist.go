package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

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
- If and only if the goal is fully achieved, start your response with "GOAL_ACHIEVED" and provide a brief summary.

Be specific and actionable. Reference concrete findings from the conversation. Do not repeat generic advice. Do not hedge — give a clear, confident direction.
Your response will be injected directly as a user message into the agent's next turn, so write it as a direct instruction to the agent.`

	userPrompt := fmt.Sprintf(`## Autopilot Goal
%s

## Conversation Context
%s

## Agent's Last Output
%s

What should the agent do next?`, goal, contextStr, lastAssistantText)

	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userPrompt}}},
	}

	resp, err := a.provider.Chat(ctx, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("strategist call failed: %w", err)
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

// extractStrategistContext builds a text-only view of the conversation for
// the strategist. It finds the compaction summary (if any) and then includes
// all subsequent messages, stripping every tool_use and tool_result block so
// only conversational text (user instructions, assistant explanations) remains.
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
				b.WriteString("\n\n--- Recent Conversation (tool calls omitted) ---\n")
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
		b.WriteString("--- Conversation (tool calls omitted) ---\n")
	}

	for _, msg := range msgs {
		var textParts []string
		for _, block := range msg.Content {
			if block.Type == "text" {
				t := strings.TrimSpace(block.Text)
				if t != "" {
					textParts = append(textParts, t)
				}
			}
		}
		if len(textParts) == 0 {
			continue
		}
		text := strings.Join(textParts, "\n")
		// Truncate very long individual messages to keep the strategist input manageable.
		const maxMsgLen = 3000
		if len(text) > maxMsgLen {
			text = text[:maxMsgLen] + "... [truncated]"
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
