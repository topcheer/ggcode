package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestIssue1014SanitizeLanChatDisplay pins the TUI-boundary sanitizer: peer
// control characters must be dropped so panel padding math (lipgloss counts
// them width 0) matches what the terminal actually renders (TABs jump to tab
// stops) - the shattered-border root cause.
func TestIssue1014SanitizeLanChatDisplay(t *testing.T) {
	cases := []struct{ in, want string }{
		{"clean text", "clean text"},
		{"ggcode-fluui-migration\t\t", "ggcode-fluui-migration"},
		{"go,\tjavascript", "go,javascript"},
		{"carriage\rreturn", "carriagereturn"},
		{"line\nbreak", "linebreak"},
		{"esc\x1b[2Jseq", "esc[2Jseq"},
		{"del\x7fchar", "delchar"},
		{"中文正常通过", "中文正常通过"},
		{"emoji ✅ pass", "emoji ✅ pass"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeLanChatDisplay(c.in); got != c.want {
			t.Errorf("sanitizeLanChatDisplay(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Fast path: clean input returns identical string (no allocation churn).
	s := "no controls here"
	if got := sanitizeLanChatDisplay(s); &s == &got {
		t.Error("fast path should return the same string for clean input")
	}
}

// TestIssue1014PadPanelLineControlChars pins the defense-in-depth layer: any
// control char reaching padPanelLine becomes a space BEFORE width math, so
// the padded line lands exactly on the border column.
func TestIssue1014PadPanelLineControlChars(t *testing.T) {
	got := padPanelLine("ab\tcd", 8)
	if strings.ContainsAny(got, "\t\r\n\x1b\x7f") {
		t.Errorf("control chars must be expanded to spaces, got %q", got)
	}
	if w := lipgloss.Width(got); w != 8 {
		t.Errorf("padded width = %d, want 8 (got %q)", w, got)
	}
	if got != "ab cd   " {
		t.Errorf("unexpected padding result %q", got)
	}
	// Wide chars still pad correctly through the control-char path.
	got2 := padPanelLine("中\t文", 8)
	if w := lipgloss.Width(got2); w != 8 {
		t.Errorf("CJK padded width = %d, want 8 (got %q)", w, got2)
	}
}
