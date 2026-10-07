package tui

// sa-86: alt+e keybinding coverage - the global key must toggle the
// second-level disclosure state of the most recent tool item.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
)

func TestAltE_TogglesToolDisclosure(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// A tool whose result far exceeds ToolBodyMaxLines - the disclosure
	// affordance ("... N more lines") is visible on its collapsed body.
	var sb strings.Builder
	for i := 0; i < chat.ToolBodyMaxLines*3; i++ {
		sb.WriteString("output line\n")
	}
	m.chatStartTool(ToolStatusMsg{
		ToolID:   "tool-1",
		ToolName: "run_command",
		Detail:   "go test ./...",
		Running:  true,
	})
	m.chatFinishTool(ToolStatusMsg{
		ToolID:   "tool-1",
		ToolName: "run_command",
		Running:  false,
		Result:   sb.String(),
	})

	item := m.chatList.FindByID("tool-1")
	if item == nil {
		t.Fatal("tool item missing from chatList")
	}
	tool := item.(interface {
		Expanded() bool
	})
	if tool.Expanded() {
		t.Fatal("tool item must start collapsed")
	}

	// alt+e expands
	m.Update(tea.KeyPressMsg{Text: "alt+e"})
	if !tool.Expanded() {
		t.Fatal("alt+e must expand the most recent tool item")
	}

	// alt+e again collapses
	m.Update(tea.KeyPressMsg{Text: "alt+e"})
	if tool.Expanded() {
		t.Fatal("second alt+e must collapse it again")
	}
}
