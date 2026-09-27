package tui

import "testing"

// #2799: knight panel approve/reject sliced id[:8] without a length guard;
// corrupted jsonl entries with short IDs panicked the TUI after the disk
// state was already written. shortProposalID must be safe for any length.
func TestShortProposalID2799(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"a", "a"},
		{"abc", "abc"},
		{"12345678", "12345678"},
		{"123456789", "12345678"},
		{"proj-0192abcdefxyz", "proj-019"},
	}
	for _, c := range cases {
		if got := shortProposalID(c.in); got != c.want {
			t.Errorf("shortProposalID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
