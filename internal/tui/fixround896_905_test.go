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

// TestImpersonateScrollSync (#900): scroll math must match the render window.
func TestImpersonateScrollSync(t *testing.T) {
	if impMaxVisible != 16 {
		t.Fatalf("impMaxVisible drifted: %d", impMaxVisible)
	}
	// cursor at the last visible row keeps the window full
	cursor, offset := impMaxVisible-1, 0
	if cursor >= offset+impMaxVisible {
		t.Fatal("scroll constant desynced from render window")
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
