//go:build darwin

package tool

import (
	"context"
	"strings"
	"testing"
)

// TestIssue2625SendKeyCtrlNonLetterFailsLoudly pins #2625: send_key with a
// non-letter single-char key + ctrl used to silently drop the modifier, write
// the bare character to the TTY, and report "ctrl+1" as sent (fake success).
// It must now fail loudly, mirroring the kitty side (sendKeyViaAction).
// The error return happens BEFORE any AppleScript/TTY I/O, so this test is
// safe in CI without iTerm2 installed.
func TestIssue2625SendKeyCtrlNonLetterFailsLoudly(t *testing.T) {
	tool := &Iterm2Tool{}
	cases := []struct {
		key        string
		mods       string
		wantSubstr string
	}{
		{"1", "ctrl", "unsupported key combo"},
		{"/", "control", "ctrl is only supported with letters"},
		{";", "ctrl,alt", "unsupported key combo"},
	}
	for _, c := range cases {
		res := tool.executeSendKey(context.Background(), "", c.key, c.mods)
		if !res.IsError {
			t.Errorf("send_key key=%q mods=%q: expected loud error, got fake success %q", c.key, c.mods, res.Content)
		}
		if !strings.Contains(res.Content, c.wantSubstr) {
			t.Errorf("send_key key=%q mods=%q: error %q missing %q", c.key, c.mods, res.Content, c.wantSubstr)
		}
	}
}
