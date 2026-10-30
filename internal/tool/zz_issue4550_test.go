//go:build darwin

package tool

// #4550: cmd/shift/meta (and any unknown modifier) have no terminal-escape
// representation in iterm2's legacy send_key path - the old code silently
// sent the bare character and reported success, injecting the letter into
// the target session (data pollution + false success). Like #2625/#4450,
// the unsupported combo must fail explicitly. The modifier validation runs
// before any TTY write, so the error cases are testable without iTerm2.

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
	// ctrl/option (and their aliases) must pass the modifier gate - the
	// rejection message must NOT appear. What happens after the gate (TTY
	// write success or failure) depends on the environment and is not
	// under test here; only the #4550 gate regression is.
	for _, mods := range []string{"ctrl", "control", "option", "alt"} {
		res := tool.executeSendKey(context.Background(), "", "a", mods)
		if strings.Contains(res.Content, "unsupported key combo: "+mods) {
			t.Fatalf("#4550 regression: %s+a must pass the modifier gate, got rejected: %s", mods, res.Content)
		}
	}
	// #2625 contract preserved: ctrl with a non-letter still rejected.
	if res := tool.executeSendKey(context.Background(), "", "1", "ctrl"); !res.IsError {
		t.Fatalf("#2625 regression: ctrl+1 must stay rejected")
	}
}
