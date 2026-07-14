package agent

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// openCall tracks a tool_use block that hasn't been closed by a matching tool_result.
type openCall struct {
	id   string
	name string
}

// ensureMessagesSendable validates and repairs the message list so it conforms
// to provider schema requirements, specifically OpenAI/Kimi-style tool-call
// pairing rules:
//   - An assistant message with tool_use blocks must be immediately followed by
//     tool_result messages matching each tool_call_id before the next assistant
//     message or the end of the conversation.
//   - Tool messages without a matching tool_call are dropped.
//
// This is a defensive final check before sending messages to the provider. It
// catches edge cases that may slip through session restore or dynamic prompt
// injections.
func (a *Agent) ensureMessagesSendable(msgs []provider.Message) []provider.Message {
	var open []openCall
	result := make([]provider.Message, 0, len(msgs))
	repaired := false

	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			// Any still-open tool calls from a previous assistant must be closed
			// before another assistant message can appear.
			if len(open) > 0 {
				result = appendSyntheticToolResults(result, open)
				open = open[:0]
				repaired = true
			}
			result = append(result, msg)
			for _, b := range msg.Content {
				if b.Type == "tool_use" {
					open = append(open, openCall{id: b.ToolID, name: b.ToolName})
				}
			}
		case "user":
			// Keep only tool_result blocks that close an open tool call.
			kept := make([]provider.ContentBlock, 0, len(msg.Content))
			for _, b := range msg.Content {
				if b.Type == "tool_result" {
					idx := indexOfOpenToolCall(open, b.ToolID)
					if idx >= 0 {
						kept = append(kept, b)
						open = append(open[:idx], open[idx+1:]...)
					} else {
						repaired = true
					}
				} else {
					kept = append(kept, b)
				}
			}
			if len(kept) > 0 || len(msg.Content) == 0 {
				result = append(result, provider.Message{Role: "user", Content: kept})
			}
		default:
			result = append(result, msg)
		}
	}

	if len(open) > 0 {
		result = appendSyntheticToolResults(result, open)
		repaired = true
	}

	if repaired {
		debug.Log("agent", "ensureMessagesSendable: repaired message list for provider compatibility")
	}
	return result
}

func indexOfOpenToolCall(open []openCall, id string) int {
	for i, c := range open {
		if c.id == id {
			return i
		}
	}
	return -1
}

func appendSyntheticToolResults(msgs []provider.Message, open []openCall) []provider.Message {
	var content []provider.ContentBlock
	for _, c := range open {
		name := c.name
		if name == "" {
			name = "unknown"
		}
		content = append(content, provider.ToolResultNamedBlock(
			c.id, name,
			"operation cancelled - tool call was interrupted before it could complete",
			true,
		))
	}
	if len(content) > 0 {
		msgs = append(msgs, provider.Message{
			Role:    "user",
			Content: content,
		})
	}
	return msgs
}
