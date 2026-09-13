package tui

import (
	"strings"
	"testing"
)

// #2178 family: positional create-echo masking across all IM panels.
// Each panel's spec format (confirmed from the create*AdapterCmd parsers)
// and the field index where secret material starts.

func TestMaskPositionalCreateEcho(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		secretFrom int
		want       string
	}{
		{"tg spec masks token", "mybot 123456:ABC-DEF", 1, "mybot ****-DEF"},
		{"signal spec keeps first two", "bot http://x +15551234", 2, "bot http://x ****1234"},
		{"name only untouched", "mybot", 1, "mybot"},
		{"fewer fields than secretFrom", "bot url", 2, "bot url"},
		{"short secret fully masked", "b tok", 1, "b ****"},
		{"negative secretFrom clamps", "a b c", -1, "**** **** ****"},
	}
	for _, tt := range tests {
		if got := maskPositionalCreateEcho(tt.input, tt.secretFrom); got != tt.want {
			t.Errorf("%s: maskPositionalCreateEcho(%q,%d)=%q want %q", tt.name, tt.input, tt.secretFrom, got, tt.want)
		}
	}
}

func TestMaskCreateEchoIndices(t *testing.T) {
	// twitch: `name token nick channels` - only the 2nd field (oauth token)
	in := "bot oauth:abc123xyz nick9 #chan"
	got := maskCreateEchoIndices(in, 1)
	if strings.Contains(got, "oauth:abc123xyz") {
		t.Errorf("twitch token leaked: %q", got)
	}
	if !strings.Contains(got, "bot") || !strings.Contains(got, "nick9") || !strings.Contains(got, "#chan") {
		t.Errorf("non-sensitive twitch fields must stay readable: %q", got)
	}
	// nostr: `name [private_key] [relays]` - only the 2nd field (private key)
	in = "bot 63 hexrelay wss://relay.damus.io"
	got = maskCreateEchoIndices(in, 1)
	if strings.Contains(got, "63") {
		t.Errorf("nostr private key leaked: %q", got)
	}
	if !strings.Contains(got, "wss://relay.damus.io") {
		t.Errorf("nostr relays must stay readable: %q", got)
	}
	// out-of-range index is a no-op
	if got := maskCreateEchoIndices("only", 3); got != "only" {
		t.Errorf("out-of-range index changed input: %q", got)
	}
}

// TestIssue2178FamilyPanels pins every panel's secret start index against
// its parser-confirmed spec format. The secret literal must not appear in
// the masked echo; the adapter name must stay readable.
func TestIssue2178FamilyPanels(t *testing.T) {
	tests := []struct {
		panel     string
		input     string
		secret    string
		clearName string
	}{
		{"tg", "mybot 123456789:AAFFtoken", "AAFFtoken", "mybot"},
		{"discord", "mybot MTE0Nzk-token", "MTE0Nzk-token", "mybot"},
		{"dingtalk", "bot dingkey dingsecret", "dingsecret", "bot"},
		{"feishu", "bot cli_a1 feishusecret", "feishusecret", "bot"},
		{"wecom", "bot WWID123 wecomsecret", "wecomsecret", "bot"},
		{"mattermost", "bot https://mm.example.com mmtoken", "mmtoken", "bot"},
		{"matrix", "bot https://hs.example.com sytoken", "sytoken", "bot"},
	}
	for _, tt := range tests {
		t.Run(tt.panel, func(t *testing.T) {
			got := maskPositionalCreateEcho(tt.input, secretFromFor(tt.panel))
			if strings.Contains(got, tt.secret) {
				t.Errorf("%s: secret %q leaked in %q", tt.panel, tt.secret, got)
			}
			if !strings.Contains(got, tt.clearName) {
				t.Errorf("%s: name %q must stay readable, got %q", tt.panel, tt.clearName, got)
			}
		})
	}
}

// secretFromFor mirrors the per-panel constants wired into the render
// calls (keep in sync with the bot_input lines in each *_panel.go).
func secretFromFor(panel string) int {
	switch panel {
	case "tg", "discord", "dingtalk":
		return 1 // name <secret...> from field 2 (dingtalk app_key hits the key family)
	case "feishu", "wecom", "mattermost", "matrix":
		return 2 // name <public-id/url> <secret...>
	default:
		return 1
	}
}
