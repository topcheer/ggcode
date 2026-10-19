package im

import "testing"

// #4322: RE2's \b never matches after a username ending in "-" or "." (both
// legal per Mattermost's ^[a-z0-9.\-_]+$ charset), so hasMention/stripMention
// silently dropped those mentions. These tests pin the post-match boundary
// fix and the #963 short-name guard it must preserve.

func TestIssue4322HasMentionTrailingDashOrDot(t *testing.T) {
	a := &mattermostAdapter{botUsername: "dev-", botUserID: "abc123"}

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"trailing dash username", "@dev- 帮我看下日志", true},
		{"trailing dash at end of text", "@dev-", true},
		{"uppercase mention", "@DEV- x", true},
		{"trailing dot is not our bot", "@ops1. hello", false}, // "ops1." bot covered below
		{"user id mention", "@abc123 ping", true},
		{"embedded not a mention", "email dev@example.com", false},
		{"plain text", "hello world", false},
	}

	for _, c := range cases {
		if got := a.hasMention(c.text); got != c.want {
			t.Errorf("%s: hasMention(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}

	// Dedicated trailing-dot adapter.
	b := &mattermostAdapter{botUsername: "ops1."}
	if !b.hasMention("@ops1. hello") {
		t.Error("hasMention must match a username ending in '.'")
	}
	if b.hasMention("@ops1.xyz hello") {
		t.Error("hasMention must NOT fire when '.' is a prefix of a longer username")
	}
}

// #963 regression: bot name "al" must not fire on "@alex".
func TestIssue4322ShortNameGuardPreserved(t *testing.T) {
	a := &mattermostAdapter{botUsername: "al"}

	if a.hasMention("@alex hello") {
		t.Error("short name 'al' must not match '@alex' (#963)")
	}
	if !a.hasMention("@al hello") {
		t.Error("'@al' alone must match")
	}
	if a.hasMention("@alice.") {
		t.Error("'@alice.' must not match bot 'al' (continuation bytes)")
	}
}

func TestIssue4322StripMention(t *testing.T) {
	a := &mattermostAdapter{botUsername: "dev-", botUserID: "abc123"}

	cases := []struct {
		name string
		text string
		want string
	}{
		{"trailing dash stripped", "@dev- 帮我看下日志", "帮我看下日志"},
		{"uppercase mention stripped, body case kept", "@DEV- Keep This", "Keep This"},
		{"user id stripped", "@abc123 ping me", "ping me"},
		{"longer username not carved (#963)", "@alex is here", "@alex is here"},
		{"no mention untouched", "plain text", "plain text"},
		// Double space is pre-existing semantics (old regex left it too);
		// only the mention itself is removed mid-text.
		{"mid-text mention stripped", "hey @dev- check this", "hey  check this"},
	}

	for _, c := range cases {
		if got := a.stripMention(c.text); got != c.want {
			t.Errorf("%s: stripMention(%q) = %q, want %q", c.name, c.text, got, c.want)
		}
	}
}
