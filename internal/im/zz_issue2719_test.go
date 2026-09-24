package im

// Issue #2719 probe: the localPart branch of matrix hasMention used a bare
// substring Contains (no "@" prefix, no word boundary), so a bot named "al"
// fired on "@alex", "usually", "also"... (#963 fixed the same class for
// mattermost; matrix was never ported). stripMention's localPart regex had
// "@" but no \b, mangling "@alex" into "ex".

import "testing"

func TestIssue2719ShortLocalPartRequiresBoundary(t *testing.T) {
	a := &matrixAdapter{userID: "@al:example.com"}
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"real mention passes", "@al please review the plan", true},
		{"longer handle is not this bot", "请问 @alex 这个方案可行吗", false},
		{"substring word not a mention", "this is the usual final call", false},
		{"full userID still matches", "ping @al:example.com now", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.hasMention(tt.body, map[string]any{}); got != tt.want {
				t.Errorf("hasMention(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestIssue2719StripMentionWordBoundary(t *testing.T) {
	a := &matrixAdapter{userID: "@al:example.com"}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"longer handle untouched", "@alex 的方案", "@alex 的方案"},
		{"real mention stripped", "@al thanks", "thanks"},
		{"substring word untouched", "usually final", "usually final"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.stripMention(tt.input); got != tt.want {
				t.Errorf("stripMention(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
