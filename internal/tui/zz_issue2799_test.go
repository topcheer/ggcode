package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2799ShortProposalID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"abcdef1234567890", "abcdef12"},
		{"abc", "abc"},
		{"12345678", "12345678"},
		{"123456789", "12345678"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := shortProposalID(tc.in); got != tc.want {
			t.Errorf("shortProposalID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIssue2799NoUnguardedIDSlice(t *testing.T) {
	src, err := os.ReadFile("knight_panel.go")
	if err != nil {
		t.Fatalf("read knight_panel.go: %v", err)
	}
	s := string(src)

	var codeLines []string
	for _, l := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		codeLines = append(codeLines, l)
	}
	code := strings.Join(codeLines, "\n")

	if n := strings.Count(code, "id[:8]"); n != 1 {
		t.Errorf("id[:8] appears %d times in code, want exactly 1 (inside shortProposalID only)", n)
	}

	n := strings.Count(s, "shortProposalID(")

	if n < 4 {
		t.Errorf("shortProposalID references = %d, want >= 4 (decl + 3 call sites)", n)
	}
}
