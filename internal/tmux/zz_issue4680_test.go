package tmux

import (
	"strings"
	"testing"
)

func TestIssue4680_ShellCommandSingleQuoted(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	got := shellCommand(`echo $(date +%s) '$HOME'`)

	want := `/bin/zsh -lc 'echo $(date +%s) '"'"'$HOME'"'"''`
	if got != want {
		t.Fatalf("shellCommand = %q, want %q (#4680)", got, want)
	}

	if strings.Contains(got, `"echo`) {
		t.Fatalf("must not use Go strconv double quoting: %q (#4680)", got)
	}
}

func TestIssue4680_PlainCommandQuoted(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	got := shellCommand("make test")
	if got != `/bin/bash -lc 'make test'` {
		t.Fatalf("plain command rendering = %q (#4680)", got)
	}
}
