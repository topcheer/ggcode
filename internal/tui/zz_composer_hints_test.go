package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// User request (2026-10): the onboarding-style composer hints
// (help / word count / Ctrl+R sidebar / Shift+Tab mode / Ctrl+C) auto-hide
// 5 minutes after startup and never come back. Hide priority is the highest:
// once the grace window has passed, the Ctrl+R sidebar toggle's own
// show/hide logic must NOT re-show the hint entry.

func TestComposerHintsAutoHideAfterFiveMinutes(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)

	// Grace period: hints visible (sidebar hidden -> Ctrl+R hint shown).
	m.sidebarVisible = false
	grace := stripAnsi(m.renderComposerPanel())
	for _, want := range []string{
		m.t("hint.help"),
		m.t("hint.shift_tab_mode"),
		m.t("hint.ctrlc_exit"),
		m.t("hint.ctrlr_sidebar"),
	} {
		if !strings.Contains(grace, want) {
			t.Fatalf("grace period: hint %q missing from composer panel", want)
		}
	}

	// Five minutes passed: all five static onboarding hints gone...
	m.hintsShownAt = time.Now().Add(-6 * time.Minute)
	suppressed := stripAnsi(m.renderComposerPanel())
	for _, gone := range []string{
		m.t("hint.help"),
		m.t("hint.shift_tab_mode"),
		m.t("hint.ctrlc_exit"),
		m.t("hint.ctrlr_sidebar"),
		"words",
	} {
		if strings.Contains(suppressed, gone) {
			t.Fatalf("after 5min: hint %q still rendered", gone)
		}
	}

	// ...but the status line itself (mode badge) survives.
	if !strings.Contains(suppressed, m.t("hint.mode")) {
		t.Fatal("after 5min: mode badge should still render")
	}
}

func TestComposerHintsHiddenBeatsCtrlRToggle(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.hintsShownAt = time.Now().Add(-6 * time.Minute)

	// Both sidebar states must render no Ctrl+R hint once suppressed --
	// the toggle's show/hide logic cannot resurrect it.
	for i := 0; i < 2; i++ {
		model, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+r"})
		m = model.(Model)
		if got := stripAnsi(m.renderComposerPanel()); strings.Contains(got, m.t("hint.ctrlr_sidebar")) {
			t.Fatalf("sidebarVisible=%v: Ctrl+R hint resurrected after grace window", m.sidebarVisible)
		}
	}
}
