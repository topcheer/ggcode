package tui

import (
	"strings"
	"testing"
)

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

	in := "bot oauth:abc123xyz nick9 #chan"
	got := maskCreateEchoIndices(in, 1)
	if strings.Contains(got, "oauth:abc123xyz") {
		t.Errorf("twitch token leaked: %q", got)
	}
	if !strings.Contains(got, "bot") || !strings.Contains(got, "nick9") || !strings.Contains(got, "#chan") {
		t.Errorf("non-sensitive twitch fields must stay readable: %q", got)
	}

	in = "bot 63 hexrelay wss://relay.damus.io"
	got = maskCreateEchoIndices(in, 1)
	if strings.Contains(got, "63") {
		t.Errorf("nostr private key leaked: %q", got)
	}
	if !strings.Contains(got, "wss://relay.damus.io") {
		t.Errorf("nostr relays must stay readable: %q", got)
	}

	if got := maskCreateEchoIndices("only", 3); got != "only" {
		t.Errorf("out-of-range index changed input: %q", got)
	}
}

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
		{"qq", "qq-main 123456 appsecret-value", "appsecret-value", "qq-main"},
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

func secretFromFor(panel string) int {
	switch panel {
	case "tg", "discord", "dingtalk":
		return 1
	case "feishu", "wecom", "mattermost", "matrix", "qq":
		return 2
	default:
		return 1
	}
}
