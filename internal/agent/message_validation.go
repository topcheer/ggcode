package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

type openCall struct {
	id   string
	name string
}

func recordGuidanceForOpenToolCalls(msg provider.Message, open []openCall, deferred *[]provider.ContentBlock, repaired *bool) {
	if len(open) == 0 {
		return
	}
	for _, b := range msg.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			*deferred = append(*deferred, b)
		}
	}
	if len(*deferred) > 0 {
		*repaired = true
	}
}

func (a *Agent) ensureMessagesSendable(msgs []provider.Message) []provider.Message {
	var open []openCall
	result := make([]provider.Message, 0, len(msgs))
	repaired := false
	var deferredGuidance []provider.ContentBlock

	flushDeferred := func() {
		if len(deferredGuidance) == 0 {
			return
		}
		if n := len(result); n > 0 && result[n-1].Role == "user" && len(result[n-1].Content) > 0 &&
			result[n-1].Content[len(result[n-1].Content)-1].Type == "tool_result" {
			result[n-1].Content = append(result[n-1].Content, deferredGuidance...)
		} else {
			result = append(result, provider.Message{Role: "user", Content: deferredGuidance})
		}
		deferredGuidance = nil
	}

	stripImages := !a.SupportsVision()
	strippedImages := 0

	for _, msg := range msgs {

		if stripImages && msg.Role == "user" {
			var stripped int
			msg, stripped = stripToolResultImages(msg)
			strippedImages += stripped
			msg, stripped = stripInlineImageBlocks(msg)
			strippedImages += stripped
		}

		switch msg.Role {
		case "assistant":
			var closedOpen bool
			result, open, closedOpen = appendAssistantMsg(result, msg, open)
			repaired = repaired || closedOpen
		case "user":

			hasToolResult := containsToolResultBlock(msg.Content)
			if len(open) > 0 && !hasToolResult {
				recordGuidanceForOpenToolCalls(msg, open, &deferredGuidance, &repaired)
				continue
			}

			kept, updatedOpen, droppedOrphans := filterToolResults(msg.Content, open)
			open = updatedOpen
			repaired = repaired || droppedOrphans
			if len(kept) > 0 || len(msg.Content) == 0 {
				result = append(result, provider.Message{Role: "user", Content: kept})
			}

			if hasToolResult && len(open) == 0 {
				flushDeferred()
			}
		default:
			result = append(result, msg)
		}
	}

	if len(open) > 0 {
		result = appendSyntheticToolResults(result, open)
		repaired = true
	}

	flushDeferred()

	if repaired {
		debug.Log("agent", "ensureMessagesSendable: repaired message list for provider compatibility")
	}
	if strippedImages > 0 {
		debug.Log("agent", "ensureMessagesSendable: stripped images from %d tool_result blocks (vision not supported)", strippedImages)
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

func appendAssistantMsg(result []provider.Message, msg provider.Message, open []openCall) ([]provider.Message, []openCall, bool) {
	closedOpen := false
	if len(open) > 0 {
		result = appendSyntheticToolResults(result, open)
		open = open[:0]
		closedOpen = true
	}
	result = append(result, msg)
	for _, b := range msg.Content {
		if b.Type == "tool_use" {
			open = append(open, openCall{id: b.ToolID, name: b.ToolName})
		}
	}
	return result, open, closedOpen
}

func containsToolResultBlock(content []provider.ContentBlock) bool {
	for _, b := range content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func filterToolResults(content []provider.ContentBlock, open []openCall) (kept []provider.ContentBlock, updated []openCall, droppedOrphans bool) {
	kept = make([]provider.ContentBlock, 0, len(content))
	for _, b := range content {
		if b.Type == "tool_result" {
			idx := indexOfOpenToolCall(open, b.ToolID)
			if idx >= 0 {
				kept = append(kept, b)
				open = append(open[:idx], open[idx+1:]...)
			} else {
				droppedOrphans = true
			}
		} else {
			kept = append(kept, b)
		}
	}
	return kept, open, droppedOrphans
}

func stripInlineImageBlocks(msg provider.Message) (provider.Message, int) {
	hasImage := false
	for _, b := range msg.Content {
		if b.Type == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return msg, 0
	}
	content := make([]provider.ContentBlock, 0, len(msg.Content))
	stripped := 0
	for _, b := range msg.Content {
		if b.Type == "image" {
			stripped++
			continue
		}
		content = append(content, b)
	}
	if len(content) == 0 {
		content = append(content, provider.TextBlock("[image removed: current model has no vision support]"))
	}
	return provider.Message{Role: msg.Role, Content: content}, stripped
}

func stripToolResultImages(msg provider.Message) (provider.Message, int) {
	var content []provider.ContentBlock
	stripped := 0
	for _, b := range msg.Content {
		if b.Type == "tool_result" && len(b.Images) > 0 {
			stripped++

			content = append(content, provider.ContentBlock{
				Type:     b.Type,
				ToolID:   b.ToolID,
				ToolName: b.ToolName,
				Output:   b.Output,
				IsError:  b.IsError,
			})
		} else {
			content = append(content, b)
		}
	}
	return provider.Message{Role: msg.Role, Content: content}, stripped
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
