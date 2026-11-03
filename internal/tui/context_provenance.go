package tui

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

var externalProvenanceTools = map[string]bool{
	"web_fetch":           true,
	"web_search":          true,
	"browser":             true,
	"read_mcp_resource":   true,
	"lanchat":             true,
	"send_message":        true,
	"a2a_remote":          true,
	"a2a_send_task":       true,
	"a2a_get_task":        true,
	"a2a_list_tasks":      true,
	"send_file_to_mobile": true,
	"delegate":            true,
	"teammate_results":    true,
	"wait_agent":          true,
}

const injectionMarker = "--- BEGIN UNTRUSTED CONTENT ---"

func contextProvenance(messages []provider.Message) (extMsgs, extTokens, flagged int) {
	for _, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		isExternal := false
		isFlagged := false
		for _, b := range msg.Content {
			if b.Type != "tool_result" {
				continue
			}
			if externalProvenanceTools[b.ToolName] {
				isExternal = true
			}
			if strings.Contains(b.Output, injectionMarker) {
				isFlagged = true
			}
		}
		if isExternal {
			extMsgs++
			extTokens += estimateMessageTokens(msg)
		}
		if isFlagged {
			flagged++
		}
	}
	return extMsgs, extTokens, flagged
}
