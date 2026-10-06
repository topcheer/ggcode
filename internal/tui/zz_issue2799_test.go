package tui

import (
	"os"
	"strings"
	"testing"
)

// zz_issue2799_test.go guards against the knight panel short-ID panic (#2799):
// the approve/reject success path sliced id[:8] unguarded, so a proposal with
// a short ID (corrupted/hand-edited project-proposals jsonl) killed the TUI
// after the status write had already succeeded.

// TestIssue2799ShortProposalID exercises the shared helper directly.
func TestIssue2799ShortProposalID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"abcdef1234567890", "abcdef12"}, // normal long ID truncated
		{"abc", "abc"},                   // short ID returned unchanged (used to panic upstream)
		{"12345678", "12345678"},         // exactly 8 unchanged
		{"123456789", "12345678"},        // 9 truncated to 8
		{"", ""},                         // empty safe
	}
	for _, tc := range cases {
		if got := shortProposalID(tc.in); got != tc.want {
			t.Errorf("shortProposalID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIssue2799NoUnguardedIDSlice pins the invariant source-level: no
// unguarded id[:8] slicing remains in knight_panel.go, and all three display
// sites route through shortProposalID.
func TestIssue2799NoUnguardedIDSlice(t *testing.T) {
	src, err := os.ReadFile("knight_panel.go")
	if err != nil {
		t.Fatalf("read knight_panel.go: %v", err)
	}
	s := string(src)
	// Count id[:8] on code lines only (strip // comments - the helper's own
	// doc comment mentions the old bug and would inflate the count).
	var codeLines []string
	for _, l := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		codeLines = append(codeLines, l)
	}
	code := strings.Join(codeLines, "\n")
	// The helper itself contains exactly one legitimate id[:8] (its truncation
	// body). Any occurrence beyond that is an unguarded call site recurrence.
	if n := strings.Count(code, "id[:8]"); n != 1 {
		t.Errorf("id[:8] appears %d times in code, want exactly 1 (inside shortProposalID only)", n)
	}
	// Both action sites and the render site must use the helper.
	n := strings.Count(s, "shortProposalID(")
	// 1 func decl + 3 call sites (approve, reject, render)
	if n < 4 {
		t.Errorf("shortProposalID references = %d, want >= 4 (decl + 3 call sites)", n)
	}
}
