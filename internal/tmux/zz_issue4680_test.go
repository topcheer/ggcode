package tmux

import (
	"strings"
	"testing"
)

// #4680: the trailing shell-command must be POSIX single-quoted so the
// OUTER tmux default-shell performs no expansion of $var/$(...)/backticks
// (wrong environment, second-order injection surface) - only the inner
// login shell evaluates the command, once. strconv.Quote (Go syntax, not
// shell) left $ and backticks live inside the old double quotes.
func TestIssue4680_ShellCommandSingleQuoted(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	got := shellCommand(`echo $(date +%s) '$HOME'`)
	// Exact expected rendering: the payload sits inside single quotes
	// (no outer-shell expansion of $()/backticks/$HOME), with embedded
	// single quotes escaped via the POSIX '"'"' idiom.
	want := `/bin/zsh -lc 'echo $(date +%s) '"'"'$HOME'"'"''`
	if got != want {
		t.Fatalf("shellCommand = %q, want %q (#4680)", got, want)
	}
	// Sanity: no Go-style double-quote wrapping remains.
	if strings.Contains(got, `"echo`) {
		t.Fatalf("must not use Go strconv double quoting: %q (#4680)", got)
	}
}

// A command without embedded quotes renders as one plain single-quoted run.
func TestIssue4680_PlainCommandQuoted(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	got := shellCommand("make test")
	if got != `/bin/bash -lc 'make test'` {
		t.Fatalf("plain command rendering = %q (#4680)", got)
	}
}
