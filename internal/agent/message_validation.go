package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// openCall tracks a tool_use block that hasn't been closed by a matching tool_result.
type openCall struct {
	id   string
	name string
}

// recordGuidanceForOpenToolCalls buffers a pure-text user message (injected
// guidance) while tool_use blocks are still open (#951). Batch-loop detectors
// (reversibility, scopeNarrow, strategyStagnation, ...) inject user guidance
// via contextManager.Add BEFORE the batch's tool_results are added, producing
// the illegal sequence assistant(tool_use) -> user(text) -> user(tool_result).
// Strict providers (Anthropic/OpenAI) reject that with a non-retryable 400.
// The buffered text blocks are appended AFTER the closing tool_result blocks
// of the next user message, restoring a legal sequence while preserving the
// guidance for the LLM.
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
	// #951: guidance text blocks deferred from between an open assistant
	// tool_use message and its closing tool_result message. They are appended
	// after the tool_result blocks to keep the sequence protocol-legal.
	var deferredGuidance []provider.ContentBlock

	// #951: release buffered guidance text blocks. Per #951 (Plan A) they are
	// MERGED into the just-emitted tool_results user message (after its
	// tool_result blocks) when possible — a single user message carrying
	// tool_result + guidance blocks is legal on both Anthropic and OpenAI.
	// A standalone user message is only used when there is no preceding
	// tool_results message to merge into (end-of-list fallback).
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

	// When the current provider does not support vision, strip image data
	// from tool_result content blocks in the message history. This prevents
	// legacy sessions (which may have recorded images when vision was enabled
	// or when a different endpoint was active) from causing 400 errors on
	// providers that reject MultiContent in tool role messages.
	stripImages := !a.SupportsVision()
	strippedImages := 0

	for _, msg := range msgs {
		// Deep-copy content blocks when stripping so we don't mutate the
		// caller's slice (which may be backed by the context manager).
		if stripImages && msg.Role == "user" {
			var stripped int
			msg, stripped = stripToolResultImages(msg)
			strippedImages += stripped
		}

		switch msg.Role {
		case "assistant":
			var closedOpen bool
			result, open, closedOpen = appendAssistantMsg(result, msg, open)
			repaired = repaired || closedOpen
		case "user":
			// #951: a pure-text user message injected while tool_use blocks are
			// still open cannot precede the tool_result message — defer its text
			// blocks until the tool_results arrive, then append after them.
			hasToolResult := containsToolResultBlock(msg.Content)
			if len(open) > 0 && !hasToolResult {
				recordGuidanceForOpenToolCalls(msg, open, &deferredGuidance, &repaired)
				continue
			}
			// Keep only tool_result blocks that close an open tool call.
			kept, updatedOpen, droppedOrphans := filterToolResults(msg.Content, open)
			open = updatedOpen
			repaired = repaired || droppedOrphans
			if len(kept) > 0 || len(msg.Content) == 0 {
				result = append(result, provider.Message{Role: "user", Content: kept})
			}
			// Deferred guidance (#951): once the batch's tool_results have been
			// emitted, release the buffered guidance text AFTER them so the
			// sequence stays assistant(tool_use) -> user(tool_result, text...).
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
	// #951: guidance that never got its closing tool_result message — flush
	// it as its own user message rather than dropping it silently.
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

// appendAssistantMsg handles one assistant message: any tool calls still open
// from the PREVIOUS assistant turn must be closed (synthetic tool_results)
// before a new assistant message may appear (#951 ordering). It then appends
// the message and collects the tool_use blocks this message opens. Returns the
// updated result/open slices and whether synthetic results were needed.
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

// containsToolResultBlock reports whether the content contains at least one
// tool_result block.
func containsToolResultBlock(content []provider.ContentBlock) bool {
	for _, b := range content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// filterToolResults keeps only the tool_result blocks that close an open tool
// call; orphaned tool_results (no matching open tool_use) are dropped. All
// non-tool_result blocks pass through unchanged. Returns the kept blocks, the
// updated open-call list, and whether any orphan was dropped.
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

// stripToolResultImages returns a deep copy of a user message with image data
// removed from tool_result blocks (their text output is preserved), plus the
// number of blocks stripped. The caller's slice is never mutated.
func stripToolResultImages(msg provider.Message) (provider.Message, int) {
	var content []provider.ContentBlock
	stripped := 0
	for _, b := range msg.Content {
		if b.Type == "tool_result" && len(b.Images) > 0 {
			stripped++
			// Keep the text output, drop the images.
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
