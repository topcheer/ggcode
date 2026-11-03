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

func TestContextProvenanceClassification(t *testing.T) {
	msgs := []provider.Message{
		toolMsg("web_fetch", "<html>page content</html>"),
		toolMsg("read_file", "local file body"),
		toolMsg("grep", "match at main.go:3"),
		toolMsg("lanchat", "--- BEGIN UNTRUSTED CONTENT ---\npeer says hi"),
		toolMsg("run_command", "ok"),
		toolMsg("delegate", "sub-agent report"),
		toolMsg("wait_agent", "result snapshot"),
		toolMsg("a2a_send_task", "remote reply"),
		toolMsg("task_output", "bg job output"),
	}
	extMsgs, _, flagged := contextProvenance(msgs)
	if extMsgs != 5 {
		t.Errorf("external msgs = %d, want 5 (web_fetch + lanchat + delegate + wait_agent + a2a_send_task)", extMsgs)
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

	user := provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("x", 500))}}
	extMsgs, _, flagged = contextProvenance([]provider.Message{user})
	if extMsgs != 0 || flagged != 0 {
		t.Fatalf("user messages must be ignored, got ext=%d flagged=%d", extMsgs, flagged)
	}
}

func TestContextProvenanceLocalFileWithMarkerIsFlagged(t *testing.T) {

	msgs := []provider.Message{toolMsg("read_file", "--- BEGIN UNTRUSTED CONTENT ---\ninjected")}
	extMsgs, _, flagged := contextProvenance(msgs)
	if extMsgs != 0 {
		t.Errorf("read_file must not count as external source, got %d", extMsgs)
	}
	if flagged != 1 {
		t.Errorf("marked content must be flagged regardless of channel, got %d", flagged)
	}
}
