package provider

import "strings"

// MergeSystemMessages merges all system messages into a single system message
// at the beginning of the slice, preserving the order of non-system messages.
//
// This is used by providers that require a single system message (or prefer it
// for cache stability). Anthropic and Gemini already handle multiple system
// messages internally via their top-level system parameter; OpenAI needs this
// because multiple system messages would be interspersed in the messages array.
//
// The first system message's content blocks are preserved in order, followed
// by subsequent system messages' text content. Non-text blocks (images, etc.)
// in system messages are preserved from the first message only.
func MergeSystemMessages(messages []Message) []Message {
	if len(messages) <= 1 {
		return messages
	}

	var systemBlocks []ContentBlock
	var nonSystem []Message
	hasSystem := false

	for _, m := range messages {
		if m.Role == "system" {
			hasSystem = true
			// Merge text blocks from all system messages
			for _, b := range m.Content {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					systemBlocks = append(systemBlocks, b)
				}
			}
		} else {
			nonSystem = append(nonSystem, m)
		}
	}

	if !hasSystem || len(systemBlocks) == 0 {
		return messages
	}

	// Build result: single merged system message + all non-system messages
	result := make([]Message, 0, len(nonSystem)+1)
	result = append(result, Message{Role: "system", Content: systemBlocks})
	result = append(result, nonSystem...)
	return result
}
