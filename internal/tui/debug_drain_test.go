package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDebugDrainTiming(t *testing.T) {
	m := newTestModel()
	h := startLiveProgramHarness(t, m)
	defer h.close()

	s := h.snapshot()
	t.Logf("before: focused=%v drainZero=%v ready=%v",
		s.input.Focused(), s.inputDrainUntil.IsZero(), s.inputReady)
	t.Logf("before: mode=%v", s.mode)
	t.Logf("before: cancelFunc nil=%v", s.cancelFunc == nil)

	h.send(tea.KeyPressMsg{Text: "h"})
	h.sync()
	s = h.snapshot()
	t.Logf("after h: value=%q", s.input.Value())
}
