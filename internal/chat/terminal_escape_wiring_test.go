package chat

import (
	"strings"
	"testing"
)

// sa-41: tool results are the primary untrusted-content display surface.
// SetResult must neutralize terminal control sequences (ATR-2026-00259)
// before anything reaches the screen.
func TestBaseToolItem_SetResultSanitizesTerminalEscapes(t *testing.T) {
	item := NewBaseToolItem("t1", "run_command", StatusPending, "", Styles{})
	item.SetResult("\x1b]0;evil title\x07visible output", false)
	if strings.Contains(item.result, "\x1b") {
		t.Errorf("raw escape leaked into displayed result: %q", item.result)
	}
	if strings.Contains(item.result, "evil title") {
		t.Errorf("OSC payload leaked into displayed result: %q", item.result)
	}
	if !strings.Contains(item.result, "visible output") {
		t.Errorf("benign content lost: %q", item.result)
	}
}

// Streaming bodies are rendered live while the tool runs and previously
// reached the screen completely unfiltered - both escapes and secrets.
func TestBaseToolItem_SetStreamingBodySanitizesAndRedacts(t *testing.T) {
	item := NewBaseToolItem("t2", "run_command", StatusRunning, "", Styles{})
	key := "sk-" + strings.Repeat("a", 30)
	item.SetStreamingBody("progress\x1b[2J\x1b[Hdone key=" + key)
	if strings.Contains(item.streamingBody, "\x1b") {
		t.Errorf("raw escape leaked into streaming body: %q", item.streamingBody)
	}
	if strings.Contains(item.streamingBody, key) {
		t.Errorf("secret leaked into streaming body: %q", item.streamingBody)
	}
}
