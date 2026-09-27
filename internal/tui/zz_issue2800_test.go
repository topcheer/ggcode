package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/stream"
)

// zz_issue2800_test.go guards against the stream panel customMode residue (#2800):
// Esc during a custom add left customMode=true with residual name/url inputs, so a
// later "e" key edit was hijacked into appending a phantom target (or erroring
// "All fields required" while silently keeping the key write).

// TestIssue2800EscResetsCustomMode drives the panel update handler: start a
// custom add, type a name, press Esc, then verify customMode is false and the
// residual inputs are cleared.
func TestIssue2800EscResetsCustomMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.streamPanel = newStreamPanel(stream.StreamConfig{})
	p := m.streamPanel
	// Simulate the custom-add entry state (what handleStreamPanelEnter does at
	// the "+ Custom..." row): customMode on, editing the name field.
	p.customMode = true
	p.editingField = "name"
	p.nameInput.SetValue("mybot")
	p.urlInput.SetValue("rtmp://residual")

	m.updateStreamPanel(tea.KeyPressMsg{Code: tea.KeyEscape})

	if p.customMode {
		t.Error("customMode still true after Esc (#2800 recurrence)")
	}
	if p.editingField != "" {
		t.Errorf("editingField not cleared after Esc: %q", p.editingField)
	}
	if v := p.nameInput.Value(); v != "" {
		t.Errorf("nameInput residual after Esc: %q", v)
	}
	if v := p.urlInput.Value(); v != "" {
		t.Errorf("urlInput residual after Esc: %q", v)
	}
}

// TestIssue2800NoSelfAssignmentNoop pins the source-level invariant: the
// self-assignment no-op removed by the fix does not come back.
func TestIssue2800NoSelfAssignmentNoop(t *testing.T) {
	src, err := os.ReadFile("stream_panel.go")
	if err != nil {
		t.Fatalf("read stream_panel.go: %v", err)
	}
	if strings.Contains(string(src), "p.urlInput.SetValue(p.urlInput.Value())") {
		t.Error("url self-assignment no-op present in stream_panel.go")
	}
}
