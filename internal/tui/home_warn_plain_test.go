package tui

import (
	"os"
	"testing"
)

// TestConfirmHomeDirPlain verifies the line-oriented HOME-directory confirm
// used by the linear frontend (#4604): y/yes continue, anything else exits,
// and EOF defaults to continue (mirroring the TUI dialog's failure fallback).
func TestConfirmHomeDirPlain(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"y continues", "y\n", true},
		{"yes continues", "yes\n", true},
		{"Y continues (case-insensitive)", "Y\n", true},
		{"whitespace-padded y continues", "  y  \n", true},
		{"n exits", "n\n", false},
		{"arbitrary text exits", "let me out\n", false},
		{"empty line exits (default N)", "\n", false},
		{"EOF continues (fallback)", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreStdin := withStdinString(t, c.input)
			defer restoreStdin()
			if got := ConfirmHomeDirPlain(LangEnglish); got != c.want {
				t.Errorf("ConfirmHomeDirPlain(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// withStdinString replaces os.Stdin with a pipe carrying s and returns a
// restore function. Output (the prompt) is discarded via os.DevNull-style
// redirection into a discarded writer.
func withStdinString(t *testing.T, s string) (restore func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("cannot create pipe: %v", err)
	}
	if _, err := w.WriteString(s); err != nil {
		t.Skipf("cannot write pipe: %v", err)
	}
	_ = w.Close()
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	os.Stdin = r
	// Swallow the printed title/message/prompt so test output stays clean.
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devNull
	return func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
		_ = r.Close()
		_ = devNull.Close()
	}
}
