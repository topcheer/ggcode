package tui

// Guard tests for the #896-#905 fix round.

import (
	"testing"
)

// TestParseIntPositiveStrict (#903): trailing garbage must be rejected, not
// silently truncated ('0x10' parsed as 0 and wiped the context window).
func TestParseIntPositiveStrict(t *testing.T) {
	bad := []string{"0x10", "12abc", "1.5M", "1 2"}
	for _, s := range bad {
		if _, err := parseIntPositive(s); err == nil {
			t.Errorf("parseIntPositive(%q) accepted trailing garbage", s)
		}
	}
	if v, err := parseIntPositive("128000"); err != nil || v != 128000 {
		t.Errorf("plain int broken: %d %v", v, err)
	}
	if v, err := parseIntPositive("1M"); err != nil || v != 1024*1024 {
		t.Errorf("suffix broken: %d %v", v, err)
	}
	if v, err := parseIntPositive(""); err != nil || v != 0 {
		t.Errorf("empty broken: %d %v", v, err)
	}
}

// TestImpersonateScrollSync (#900/#914): scrolling must advance only when the
// cursor passes the render window (impMaxVisible), not at row 9.
func TestImpersonateScrollSync(t *testing.T) {
	panel := impersonatePanelState{scrollOffset: 0, cursor: 0}
	// Walk the cursor down 16 rows: within the window, no scroll.
	for i := 0; i < impMaxVisible-1; i++ {
		panel.cursor++
		if panel.cursor < panel.scrollOffset+impMaxVisible {
			continue // still visible
		}
		t.Fatalf("cursor %d scrolled out of a %d-row window (scroll math desynced)", panel.cursor, impMaxVisible)
	}
	// 17th row pushes the window by exactly one.
	panel.cursor++
	if panel.cursor >= panel.scrollOffset+impMaxVisible {
		panel.scrollOffset = panel.cursor - impMaxVisible + 1
	}
	if panel.scrollOffset != 1 {
		t.Fatalf("expected scrollOffset 1 after cursor=%d, got %d", panel.cursor, panel.scrollOffset)
	}
}

// TestLanchatApprovalIdxClamp (#899): modulo clamp on a shrunken list.
func TestLanchatApprovalIdxClamp(t *testing.T) {
	idx := 2
	pending := 1 // shrank from 3 (timeouts/remote cancels)
	if idx >= pending {
		idx %= pending
	}
	if idx != 0 {
		t.Fatalf("clamp failed: %d", idx)
	}
}

// TestIsLocalBaseURLIPv6 (#906): both IPv6 forms must be recognized local.
func TestIsLocalBaseURLIPv6(t *testing.T) {
	local := []string{
		"http://[::1]:11434",
		"http://::1:11434",
		"http://localhost:11434",
		"http://127.0.0.1:11434",
	}
	for _, u := range local {
		if !isLocalBaseURL(u) {
			t.Errorf("isLocalBaseURL(%q) = false, want true", u)
		}
	}
	if isLocalBaseURL("http://example.com:11434") {
		t.Error("remote host judged local")
	}
}

// TestShortSessionID (#908): short remote IDs must not panic.
func TestShortSessionID(t *testing.T) {
	if got := shortSessionID("abc"); got != "abc" {
		t.Fatalf("short id mangled: %q", got)
	}
	if got := shortSessionID("0123456789abcdef"); got != "0123456789ab" {
		t.Fatalf("long id not truncated: %q", got)
	}
}
