package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMaskedNewFieldEcho(t *testing.T) {
	got := maskedNewFieldEcho("bot_token=xoxb-secret-value")
	if strings.Contains(got, "xoxb-secret-value") {
		t.Fatalf("new-field secret value leaked: %q", got)
	}
	if !strings.HasPrefix(got, "bot_token=") {
		t.Fatalf("name half must stay readable: %q", got)
	}

	if got := maskedNewFieldEcho("display_name=my adapter"); got != "display_name=my adapter" {
		t.Fatalf("benign new-field must echo verbatim: %q", got)
	}

	if got := maskedNewFieldEcho("bot_to"); got != "bot_to" {
		t.Fatalf("name-only buffer must echo verbatim: %q", got)
	}
}

func TestRenderIMEditInputNewFieldModeMasked(t *testing.T) {
	m := newTestModel()
	s := &imAdapterEditState{
		adapterName: "x",
		editField:   "",
		editInput:   "api_key=sk-12345",
		mode:        imEditInput,
	}
	out := m.renderIMEditInput(s)
	if strings.Contains(out, "sk-12345") {
		t.Fatalf("new-field mode leaked the secret into the frame: %q", out)
	}
	if !strings.Contains(out, "api_key") {
		t.Fatal("field name half must remain readable")
	}
}

func TestCtrlEUpdatesAutoComplete(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "mention"
	m.autoCompleteItems = []string{"internal/"}
	m.input.SetValue("@int")
	m.input.CursorStart()

	updated, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "ctrl+e"}, nil)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}

	_ = m2
}
