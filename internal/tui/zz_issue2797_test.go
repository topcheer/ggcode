package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2797PanelCreateGuards(t *testing.T) {
	cases := []struct {
		file string
		max  string
		msg  string
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
