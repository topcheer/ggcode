package im

import "testing"

// #4322 companion test: RE2 \b never fires after a username legally ending in
// '-' or '.', so hasMention/stripMention silently dropped non-DM mentions of
// such bots. The boundary is now a post-match continuation-char check
// (mattermostUsernameContChars), mirroring the matrix #2749 fix pattern.

func zzMentionAdapter(username, userID string) *mattermostAdapter {
	return &mattermostAdapter{botUsername: username, botUserID: userID}
}

func TestZZMattermostMentionBoundaryTrailingDashDot(t *testing.T) {
	cases := []struct {
		name     string
		username string
		text     string
		want     bool
	}{
		// Regression core: names ending in '-' or '.' must match (#4322).
		{"trailing dash then space", "dev-", "@dev- 帮我看下日志", true},
		{"trailing dash at end", "dev-", "ping @dev-", true},
		{"trailing dot then space", "ops1.", "@ops1. check logs", true},
		{"trailing dot at end", "a.", "hey @a.", true},
		{"mixed case mention", "dev-", "@DEV- look", true},
		// Boundary must still hold where it always did (#963).
		{"short name not substring", "al", "@alex hello", false},
		{"continuation blocks match", "dev", "@dev- team note", false}, // "@dev-" is a different, longer name
		{"dash continuation blocks", "dev", "@dev. status", false},
		{"underscore continuation blocks", "dev", "@dev_ops", false},
		{"plain mention still true", "dev", "@dev run tests", true},
		{"no mention", "dev", "dev unmentioned", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := zzMentionAdapter(tc.username, "")
			if got := a.hasMention(tc.text); got != tc.want {
				t.Fatalf("hasMention(%q, bot=%q) = %v, want %v", tc.text, tc.username, got, tc.want)
			}
		})
	}
}

func TestZZMattermostMentionBoundaryUserID(t *testing.T) {
	a := zzMentionAdapter("bot", "abc123xyz")
	if !a.hasMention("@abc123xyz go") {
		t.Fatal("user-ID mention must match")
	}
	if a.hasMention("@abc123xyz9 go") { // ID continued by alnum is a different ID
		t.Fatal("continuation char must block user-ID match")
	}
}

func TestZZMattermostStripMentionTrailingDashDot(t *testing.T) {
	cases := []struct {
		name     string
		username string
		in       string
		want     string
	}{
		{"strip trailing dash", "dev-", "@dev- 帮我看下日志", "帮我看下日志"},
		{"strip trailing dot", "ops1.", "@ops1. check logs", "check logs"},
		{"strip mixed case", "dev-", "@DEV- look", "look"},
		{"short name keeps @alex", "al", "@alex hello", "@alex hello"},
		{"strip plain name", "dev", "@dev run tests", "run tests"},
		{"username strip leaves other mention", "bot", "@bot @abc123 go", "@abc123 go"}, // userID empty: only @bot stripped
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := zzMentionAdapter(tc.username, "")
			if got := a.stripMention(tc.in); got != tc.want {
				t.Fatalf("stripMention(%q, bot=%q) = %q, want %q", tc.in, tc.username, got, tc.want)
			}
		})
	}
}

func TestZZMattermostStripMentionUserID(t *testing.T) {
	a := zzMentionAdapter("", "abc123xyz")
	if got := a.stripMention("@abc123xyz go"); got != "go" {
		t.Fatalf("user-ID strip = %q, want \"go\"", got)
	}
}

// Multi-byte text right after the mention must count as a boundary byte-wise
// (high bytes are not in the ASCII continuation set), matching matrix #3572
// single-coordinate-system behavior.
func TestZZMattermostMentionBoundaryMultibyte(t *testing.T) {
	a := zzMentionAdapter("dev-", "")
	if !a.hasMention("@dev-你好") {
		t.Fatal("mention followed by multibyte text must match")
	}
	if got := a.stripMention("@dev-你好"); got != "你好" {
		t.Fatalf("strip with multibyte suffix = %q, want 你好", got)
	}
}
