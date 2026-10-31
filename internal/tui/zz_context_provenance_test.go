package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func toolMsg(name, output string) provider.Message {
	return provider.Message{Role: "tool", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "t1", ToolName: name, Output: output},
	}}
}

// contextProvenance classifies tool-result messages by SOURCE: external
// channels (web/peer) count, local reads do not; injection-marked messages
// are counted independently of the channel they arrived on.
func TestContextProvenanceClassification(t *testing.T) {
	msgs := []provider.Message{
		toolMsg("web_fetch", "<html>page content</html>"),                   // external
		toolMsg("read_file", "local file body"),                             // local: not external
		toolMsg("grep", "match at main.go:3"),                               // local: not external
		toolMsg("lanchat", "--- BEGIN UNTRUSTED CONTENT ---\npeer says hi"), // external + flagged
		toolMsg("run_command", "ok"),                                        // local exec: not external
	}
	extMsgs, _, flagged := contextProvenance(msgs)
	if extMsgs != 2 {
		t.Errorf("external msgs = %d, want 2 (web_fetch + lanchat)", extMsgs)
	}
	if flagged != 1 {
		t.Errorf("flagged msgs = %d, want 1 (UNTRUSTED marker in lanchat result)", flagged)
	}
}

func TestContextProvenanceEmptyAndNonTool(t *testing.T) {
	extMsgs, extTokens, flagged := contextProvenance(nil)
	if extMsgs != 0 || extTokens != 0 || flagged != 0 {
		t.Fatalf("empty input must be all-zero, got %d/%d/%d", extMsgs, extTokens, flagged)
	}
	// Non-tool roles are ignored entirely.
	user := provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("x", 500))}}
	extMsgs, _, flagged = contextProvenance([]provider.Message{user})
	if extMsgs != 0 || flagged != 0 {
		t.Fatalf("user messages must be ignored, got ext=%d flagged=%d", extMsgs, flagged)
	}
}

func TestContextProvenanceLocalFileWithMarkerIsFlagged(t *testing.T) {
	// A LOCAL read carrying injected text is flagged (risk dimension) but
	// NOT external (source dimension) - the two axes are independent.
	msgs := []provider.Message{toolMsg("read_file", "--- BEGIN UNTRUSTED CONTENT ---\ninjected")}
	extMsgs, _, flagged := contextProvenance(msgs)
	if extMsgs != 0 {
		t.Errorf("read_file must not count as external source, got %d", extMsgs)
	}
	if flagged != 1 {
		t.Errorf("marked content must be flagged regardless of channel, got %d", flagged)
	}
}
