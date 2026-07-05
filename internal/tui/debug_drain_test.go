package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDebugDrainTiming(t *testing.T) {
	m := newTestModel()

	// Direct test: call Update with KeyPressMsg
	result, _ := m.Update(tea.KeyPressMsg{Text: "h"})
	if updated, ok := result.(Model); ok {
		t.Logf("direct Update: input=%q", updated.input.Value())
	} else {
		t.Fatalf("wrong type %T", result)
	}

	// Via harness
	m2 := newTestModel()
	h := startLiveProgramHarness(t, m2)
	defer h.close()

	h.send(tea.KeyPressMsg{Text: "h"})
	h.sync()
	s := h.snapshot()
	t.Logf("via harness: input=%q", s.input.Value())

	// Check if the issue is the harness program startup
	fmt.Printf("DEBUG: harness input focused=%v\n", s.input.Focused())
}
