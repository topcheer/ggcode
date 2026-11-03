package tui

import "testing"

func TestParseTmuxEnterArgsBareSetup1764(t *testing.T) {

	sess, layout := parseTmuxEnterArgs([]string{"setup"})
	if layout != "default" {
		t.Fatalf("sole 'setup' should alias --setup, layout=%q", layout)
	}
	if sess != "" {
		t.Fatalf("sole 'setup' should not set session name, got %q", sess)
	}

	sess, layout = parseTmuxEnterArgs([]string{"setup", "extra"})
	if sess != "setup" {
		t.Fatalf("'setup extra' should treat setup as session name, got %q", sess)
	}
	if layout != "" {
		t.Fatalf("'setup extra' should not set layout, got %q", layout)
	}

	sess, layout = parseTmuxEnterArgs([]string{"--setup", "tiles"})
	if layout != "tiles" {
		t.Fatalf("--setup tiles should set layout=tiles, got %q", layout)
	}
	if sess != "" {
		t.Fatalf("--setup tiles should not set session, got %q", sess)
	}
}
