package tui

import (
	"strings"
	"testing"
)

func TestStreamConfigNonEmptyResponse(t *testing.T) {
	m := &Model{}
	resp, _ := m.handleStreamSlash("config")
	if strings.TrimSpace(resp) == "" {
		t.Fatal("/stream config returned an empty response - the IM emit gate drops it, leaving zero remote feedback (#2408 regression)")
	}
}
