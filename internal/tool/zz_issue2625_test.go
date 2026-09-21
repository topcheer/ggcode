//go:build darwin

package tool

import (
	"context"
	"strings"
	"testing"
)

// #2625: ctrl with a non-letter key (digit, symbol) has no terminal
// representation. The old code fell through with the BARE character, wrote
// it to the TTY, and reported success ("ctrl+1 sent") - a false success the
// agent builds on. The combo must be rejected up front (aligned with
// kitty's unsupported-combo error), never reaching the TTY write.
func TestIssue2625_CtrlNonLetterRejected(t *testing.T) {
	tool := &Iterm2Tool{}
	for _, key := range []string{"1", "/", ";", "5"} {
		res := tool.executeSendKey(context.Background(), "", key, "ctrl")
		if !res.IsError {
			t.Fatalf("ctrl+%s must be rejected as unsupported (was: bare key + false success, #2625): %s", key, res.Content)
		}
		if !strings.Contains(res.Content, "unsupported key combo") {
			t.Fatalf("ctrl+%s error should name the unsupported combo, got: %s", key, res.Content)
		}
	}
	// ctrl+letter is intentionally NOT asserted here: it proceeds to the
	// real TTY write (side effects); the conversion logic itself is
	// unchanged by the #2625 patch (else branch only).
}
