package provider

import "strings"

func MergeSystemMessages(messages []Message) []Message {
	if len(messages) <= 1 {
		return messages
	}

	var systemBlocks []ContentBlock
	var nonSystem []Message
	hasSystem := false
	seenFirstSystem := false

	for _, m := range messages {
		if m.Role == "system" {
			hasSystem = true
			if !seenFirstSystem {

				seenFirstSystem = true
				for _, b := range m.Content {
					if b.Type == "text" && strings.TrimSpace(b.Text) == "" {
						continue
					}
					systemBlocks = append(systemBlocks, b)
				}
				continue
			}

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

	result := make([]Message, 0, len(nonSystem)+1)
	result = append(result, Message{Role: "system", Content: systemBlocks})
	result = append(result, nonSystem...)
	return result
}
