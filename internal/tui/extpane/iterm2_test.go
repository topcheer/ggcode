package extpane

import (
	"strings"
	"testing"
)

func TestTailCommandShellSafe(t *testing.T) {
	for _, logfile := range []string{
		"/tmp/logs/O'Brien/panel.log",
		`/tmp/logs/back\slash/panel.log`,
		"/tmp/plain.log",
	} {
		safe := strings.ReplaceAll(logfile, "\\", "\\\\")
		safe = strings.ReplaceAll(safe, "'", "'\\''")
		cmd := "tail -f '" + safe + "'"

		inSingle := false
		for i := 0; i < len(cmd); i++ {
			switch cmd[i] {
			case '\'':

				if inSingle && i > 0 && cmd[i-1] == '\'' {
					continue
				}
				inSingle = !inSingle
			}
		}
		if inSingle {
			t.Errorf("path %q produced unbalanced quoting: %s", logfile, cmd)
		}
	}
}

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
