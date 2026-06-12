package im

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

func SessionHistoryEvents(messages []provider.Message) []OutboundEvent {
	events := make([]OutboundEvent, 0, len(messages))
	for _, msg := range messages {
		// Skip system messages (system prompt, project memory) — never send to IM
		if strings.EqualFold(msg.Role, "system") {
			continue
		}
		for _, line := range renderMessageLines(msg) {
			events = append(events, OutboundEvent{Kind: OutboundEventText, Text: line})
		}
	}
	return events
}

func renderMessageLines(msg provider.Message) []string {
	lines := make([]string, 0, len(msg.Content))
	role := strings.ToUpper(strings.TrimSpace(msg.Role))
	if role == "" {
		role = "MESSAGE"
	}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				lines = append(lines, fmt.Sprintf("[%s] %s", role, text))
			}
		case "image":
			lines = append(lines, fmt.Sprintf("[%s] [image attached: %s]", role, firstNonEmpty(block.ImageMIME, "unknown")))
		case "tool_use":
			input := strings.TrimSpace(string(block.Input))
			if !json.Valid(block.Input) {
				input = strings.TrimSpace(string(block.Input))
			}
			lines = append(lines, fmt.Sprintf("[%s] [tool call] %s %s", role, block.ToolName, input))
		case "tool_result":
			result := strings.TrimSpace(block.Output)
			if result == "" {
				result = "(empty)"
			}
			lines = append(lines, fmt.Sprintf("[%s] [tool result] %s", role, result))
		}
	}
	return lines
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
