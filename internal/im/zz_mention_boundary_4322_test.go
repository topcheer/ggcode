package im

import "testing"

func TestMentionBoundaryUsernameCharsetName(t *testing.T) {
	cases := []struct {
		bot     string
		name    string
		text    string
		wantHit bool
	}{
		{"dev-", "trailing dash", "@dev- help with logs", true},
		{"dev-", "trailing dash end-of-text", "ping @dev-", true},
		{"ops1.", "trailing dot", "@ops1. check logs", true},
		{"al", "plain name mention", "@al hello", true},
		{"al", "substring no false hit", "@alex hello", false},
		{"dev", "longer username no carve", "@dev-extra text", false},
		{"dev-", "no mention", "hello world", false},
	}
	for _, c := range cases {
		a := &mattermostAdapter{botUsername: c.bot}
		if got := a.hasMention(c.text); got != c.wantHit {
			t.Errorf("bot=%q case=%q hasMention(%q) = %v, want %v", c.bot, c.name, c.text, got, c.wantHit)
		}
	}
}

func TestStripMentionBoundaryPreservesSeparator(t *testing.T) {
	a := &mattermostAdapter{botUsername: "dev-"}
	if got := a.stripMention("@dev- help with logs"); got != "help with logs" {
		t.Errorf("strip = %q, want %q", got, "help with logs")
	}
	b := &mattermostAdapter{botUsername: "al"}
	if got := b.stripMention("@al hello there"); got != "hello there" {
		t.Errorf("strip = %q, want %q", got, "hello there")
	}

	if got := b.stripMention("@alex hello"); got != "@alex hello" {
		t.Errorf("strip = %q, want unchanged %q", got, "@alex hello")
	}
}
