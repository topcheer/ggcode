package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
)

func TestFollowToolResultFallbackFIFO(t *testing.T) {
	events := []followEvent{
		{Type: followEventToolCall, ToolName: "run_command"},
		{Type: followEventToolCall, ToolName: "run_command"},

		{Type: followEventToolResult, ToolName: "run_command", Result: "second's result", IsError: false},
	}
	data := followEventData{Events: events}
	list := chat.NewList(80, 24)
	buildFollowList(data, list, chat.DefaultStyles())

	if list.Len() < 2 {
		t.Fatalf("expected at least 2 list items (call1 with result + call2), got %d", list.Len())
	}
}

func TestFollowStripActiveAnchorScrolls(t *testing.T) {
	m := Model{}
	m.subAgentFollow.slots = make([]followSlot, 7)
	for i := range m.subAgentFollow.slots {
		m.subAgentFollow.slots[i] = followSlot{ID: string(rune('a' + i)), Name: string(rune('A' + i))}
	}
	m.subAgentFollow.activeID = "g"

	got := m.renderSubAgentFollowStrip()
	if !strings.Contains(got, "G") {
		t.Fatalf("#1761 case 3: active 7th slot 'G' not visible in strip:\n%s", got)
	}
	if !strings.Contains(got, "▶") {
		t.Fatalf("active marker missing in strip:\n%s", got)
	}

	if strings.Contains(got, "A │") && strings.Contains(got, "G") {

		t.Logf("window includes both A and G - inspect: %s", got)
	}

	m.subAgentFollow.activeID = "a"
	if got := m.renderSubAgentFollowStrip(); !strings.Contains(got, "▶") {
		t.Fatalf("head-active marker missing:\n%s", got)
	}
}
