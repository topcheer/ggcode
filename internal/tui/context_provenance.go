package tui

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// externalProvenanceTools are the network/peer tool channels whose results
// carry content from OUTSIDE the local workspace (web pages, browser reads,
// LAN peer messages, remote-agent responses). This is the SOURCE dimension -
// deliberately narrower than prompt_injection_guard.go's externalContentTools
// (which also includes local file reads, since any file can carry injected
// text): here we answer "where did this context come from", not "is it
// risky". Keep the channel list aligned with the guard's r406 additions.
var externalProvenanceTools = map[string]bool{
	"web_fetch":           true,
	"web_search":          true,
	"browser":             true,
	"read_mcp_resource":   true,
	"lanchat":             true,
	"send_message":        true,
	"a2a_remote":          true,
	"send_file_to_mobile": true,
}

// injectionMarker is the inline marker the prompt-injection guard wraps
// untrusted external content with (Spotlighting). Counting marked messages
// tells the user how many tool results currently carry flagged content.
const injectionMarker = "--- BEGIN UNTRUSTED CONTENT ---"

// contextProvenance walks tool-result messages and returns
// (external-source message count, their token estimate,
// injection-flagged message count).
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
