package tui

import "testing"

// #1764 case 3: "/tmux enter setup" must be able to create a session
// literally named "setup" - bare "setup" is only the --setup alias when
// it is the sole argument.
func TestParseTmuxEnterArgsBareSetup1764(t *testing.T) {
	// sole argument: alias behavior preserved
	sess, layout := parseTmuxEnterArgs([]string{"setup"})
	if layout != "default" {
		t.Fatalf("sole 'setup' should alias --setup, layout=%q", layout)
	}
	if sess != "" {
		t.Fatalf("sole 'setup' should not set session name, got %q", sess)
	}
	// two arguments: first is the session NAME
	sess, layout = parseTmuxEnterArgs([]string{"setup", "extra"})
	if sess != "setup" {
		t.Fatalf("'setup extra' should treat setup as session name, got %q", sess)
	}
	if layout != "" {
		t.Fatalf("'setup extra' should not set layout, got %q", layout)
	}
	// explicit flag still wins with a following value
	sess, layout = parseTmuxEnterArgs([]string{"--setup", "tiles"})
	if layout != "tiles" {
		t.Fatalf("--setup tiles should set layout=tiles, got %q", layout)
	}
	if sess != "" {
		t.Fatalf("--setup tiles should not set session, got %q", sess)
	}
}
