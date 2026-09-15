package tui

import (
	"strings"
	"testing"
)

// TestStreamConfigNonEmptyResponse pins #2408: the /stream config branch must
// return non-empty text - update_remote.go only emits non-empty responses, so
// an empty return left the IM side with zero feedback while the local TUI
// popped its config panel.
func TestStreamConfigNonEmptyResponse(t *testing.T) {
	m := &Model{}
	resp, _ := m.handleStreamSlash("config")
	if strings.TrimSpace(resp) == "" {
		t.Fatal("/stream config returned an empty response - the IM emit gate drops it, leaving zero remote feedback (#2408 regression)")
	}
}
