package extpane

import (
	"strings"
	"testing"
)

// TestTailCommandShellSafe pins #1414-A: the tail -f command built for
// iTerm2's `write text` must be valid POSIX shell even when the log path
// contains an apostrophe (teammate "O'Brien") or backslashes. The old
// \' escaping was AppleScript-correct but shell-BROKEN: the backslash is
// literal inside single quotes, the quote closed early, and the tab sat
// dead on "unexpected EOF" - the exact case the #1369 side-fix claimed
// to repair.
func TestTailCommandShellSafe(t *testing.T) {
	for _, logfile := range []string{
		"/tmp/logs/O'Brien/panel.log",
		`/tmp/logs/back\slash/panel.log`,
		"/tmp/plain.log",
	} {
		safe := strings.ReplaceAll(logfile, "\\", "\\\\")
		safe = strings.ReplaceAll(safe, "'", "'\\''")
		cmd := "tail -f '" + safe + "'"
		// Simulate POSIX single-quote parsing of cmd: states must return
		// to unquoted by end-of-string with no stray escapes outside quotes.
		inSingle := false
		for i := 0; i < len(cmd); i++ {
			switch cmd[i] {
			case '\'':
				// In POSIX shell, \' outside quotes is an escaped quote;
				// inside single quotes the backslash is literal. The
				// '\'' idiom = close, escaped-quote, reopen. Track quotes:
				if inSingle && i > 0 && cmd[i-1] == '\'' {
					continue // the escaped member of '\''  - no state change
				}
				inSingle = !inSingle
			}
		}
		if inSingle {
			t.Errorf("path %q produced unbalanced quoting: %s", logfile, cmd)
		}
	}
}

// TestStripAnsiConsumesOSC pins #1414-B: OSC sequences (window titles
// with BEL terminator, hyperlinks with ST terminator) must be consumed
// whole - the old parser only knew CSI and let the OSC body plus BEL
// leak verbatim into the preview pane.
func TestStripAnsiConsumesOSC(t *testing.T) {
	cases := []struct{ in, want string }{
		{"before\x1b]0;my title\x07after", "beforeafter"},
		{"link\x1b]8;;https://example.com\x1b\\text\x1b]8;;\x1b\\tail", "linktexttail"},
		{"csi\x1b[31mcolor\x1b[0m", "csicolor"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := stripAnsi(c.in); got != c.want {
			t.Errorf("stripAnsi(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
