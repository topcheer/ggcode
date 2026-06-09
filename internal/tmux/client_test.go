package tmux

import "testing"

func TestEnvironmentLabel(t *testing.T) {
	env := &Environment{InTmux: true, Session: "dev", Window: "1:code", PaneID: "%3"}
	if got, want := env.Label(), "tmux dev:1:code %3"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
}

func TestEnvironmentLabelOutsideTmux(t *testing.T) {
	env := &Environment{Available: true, InTmux: false, Session: "dev", PaneID: "%3"}
	if got := env.Label(); got != "" {
		t.Fatalf("Label() outside tmux = %q, want empty", got)
	}
}

func TestShellCommandQuotesCommand(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	got := shellCommand("echo hello; printf '%s' done")
	want := "/bin/sh -lc \"echo hello; printf '%s' done\""
	if got != want {
		t.Fatalf("shellCommand() = %q, want %q", got, want)
	}
}
