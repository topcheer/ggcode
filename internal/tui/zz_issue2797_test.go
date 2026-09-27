package tui

import (
	"os"
	"strings"
	"testing"
)

// zz_issue2797_test.go guards against a #1370-B/C recurrence in the seven IM
// panels that were fixed in #2797. Source-level invariant probe (per the
// #2793/#2794/#2795 convention): runtime reproduction of the truncation is
// trivial but the rollback path races with TUI update-loop wiring, so we pin
// the invariants directly in the source instead.
func TestIssue2797PanelCreateGuards(t *testing.T) {
	cases := []struct {
		file string
		max  string // expected upper-bound check, e.g. "> 3"
		msg  string // substring of the expected rejection error
	}{
		{"dingtalk_panel.go", "> 3", "extra fields after app_secret"},
		{"discord_panel.go", "> 2", "extra fields after token"},
		{"qq_panel.go", "> 3", "extra fields after appsecret"},
		{"irc_panel.go", "> 4", "extra fields after channels"},
		{"signal_panel.go", "> 3", "extra fields after account"},
		{"twitch_panel.go", "> 4", "extra fields after channels"},
		{"nostr_panel.go", "> 3", "extra fields after relays"},
	}
	for _, tc := range cases {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("%s: read: %v", tc.file, err)
		}
		s := string(src)

		// #1370-C guard: upper-bound rejection right after the lower-bound check.
		if !strings.Contains(s, "if len(fields) "+tc.max+" {") {
			t.Errorf("%s: missing `len(fields) %s` upper-bound rejection (#1370-C recurrence)", tc.file, tc.max)
		}
		if !strings.Contains(s, tc.msg) {
			t.Errorf("%s: missing rejection message %q", tc.file, tc.msg)
		}
		lower := strings.Index(s, "if len(fields) <")
		upper := strings.Index(s, "if len(fields) "+tc.max)
		if lower < 0 || upper < 0 || upper < lower {
			t.Errorf("%s: upper-bound check must follow the lower-bound check", tc.file)
		}

		// #1370-B guard: rollback closure in the create-path next() that
		// removes the just-persisted adapter and re-saves the config.
		if !strings.Contains(s, "rollback := func(origErr error) tea.Msg") {
			t.Errorf("%s: missing rollback closure (#1370-B recurrence)", tc.file)
		}
		if !strings.Contains(s, "m.config.RemoveIMAdapter(name)") {
			t.Errorf("%s: rollback does not RemoveIMAdapter", tc.file)
		}
		if !strings.Contains(s, "return rollback(err)") {
			t.Errorf("%s: start failure branch does not invoke rollback", tc.file)
		}
	}
}
