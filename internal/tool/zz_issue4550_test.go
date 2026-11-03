//go:build darwin

package tool

import (
	"context"
	"strings"
	"testing"
)

func TestIssue4550_UnsupportedModifiersRejected(t *testing.T) {
	tool := &Iterm2Tool{}
	for _, mods := range []string{"cmd", "command", "shift", "meta", "super", "cmd+shift", "Cmd", "SHIFT"} {
		res := tool.executeSendKey(context.Background(), "", "c", mods)
		if !res.IsError {
			t.Fatalf("#4550: %s+c must be rejected (was: bare key injection + false success): %s", mods, res.Content)
		}
		if !strings.Contains(res.Content, "unsupported key combo") {
			t.Fatalf("#4550: %s+c error should name the unsupported combo, got: %s", mods, res.Content)
		}
	}
}

func TestIssue4550_SupportedModifiersStillPassGate(t *testing.T) {
	tool := &Iterm2Tool{}

	for _, mods := range []string{"ctrl", "control", "option", "alt"} {
		res := tool.executeSendKey(context.Background(), "", "a", mods)
		if strings.Contains(res.Content, "unsupported key combo: "+mods) {
			t.Fatalf("#4550 regression: %s+a must pass the modifier gate, got rejected: %s", mods, res.Content)
		}
	}

	if res := tool.executeSendKey(context.Background(), "", "1", "ctrl"); !res.IsError {
		t.Fatalf("#2625 regression: ctrl+1 must stay rejected")
	}
}
