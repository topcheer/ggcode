package im

// #2749 regression: Matrix mention detection must handle localparts that
// LEGALLY end with non-word characters (Matrix charset [a-z0-9._=-+/]).
// The \b boundary never matched after endings like "bot-", "x=", "a." -
// hasMention returned false (message silently dropped in requireMention
// rooms) and stripMention left the raw "@bot-" prefix in the LLM prompt.
// The replacement boundary (?![a-z0-9._=\-+/]) must also keep the #2719
// guarantee: "@bot" must not match inside "@bot.a" (a DIFFERENT user's
// full mention).

import (
	"strings"
	"testing"
)

func TestIssue2749_NonWordEndingLocalpartMention(t *testing.T) {
	nonWordEndings := []string{"bot-", "x=", "dev+", "a.", "u/"}
	for _, lp := range nonWordEndings {
		a := &matrixAdapter{userID: "@" + lp + ":matrix.org"}
		body := "@" + lp + " help me check the build"
		if !a.hasMention(body, nil) {
			t.Errorf("hasMention(%q) = false for localpart %q - mention silently dropped", body, lp)
		}
		stripped := a.stripMention(body)
		if strings.Contains(stripped, "@"+lp) {
			t.Errorf("stripMention left %q in %q", "@"+lp, stripped)
		}
	}
}

func TestIssue2749_PrefixConfusionStillRefused(t *testing.T) {
	// #2719 invariant preserved: "al" must not match "@alex", and "bot"
	// must not match "@bot.a" (bot.a is a different user's localpart).
	for _, tc := range []struct{ localPart, body string }{
		{"al", "@alex hello"},
		{"bot", "@bot.a hello"},
		{"dev", "@dev+x hello"},
	} {
		a := &matrixAdapter{userID: "@" + tc.localPart + ":matrix.org"}
		if a.hasMention(tc.body, nil) {
			t.Errorf("hasMention(%q) must stay false for localpart %q (prefix of another user)", tc.body, tc.localPart)
		}
		stripped := a.stripMention(tc.body)
		if stripped != tc.body {
			t.Errorf("stripMention(%q) mangled to %q - longer handle must stay intact", tc.body, stripped)
		}
	}
}

func TestIssue2749_WordEndingLocalpartUnchanged(t *testing.T) {
	// Word-ending localparts behave exactly as before (#2719 suite).
	a := &matrixAdapter{userID: "@al:matrix.org"}
	if !a.hasMention("@al do it", nil) {
		t.Errorf("plain word-ending mention must still match")
	}
	if a.hasMention("@alex do it", nil) {
		t.Errorf("prefix confusion regression")
	}
}

func TestIssue2749_MixedCaseLocalpart(t *testing.T) {
	// #2749 case follow-up: self-hosted homeservers may issue mixed-case
	// localparts; the old (?i) regex matched them. Lower both sides.
	a := &matrixAdapter{userID: "@Bot-:matrix.org"}
	if !a.hasMention("@Bot- check the build", nil) {
		t.Errorf("mixed-case non-word-ending mention must match")
	}
	if !a.hasMention("hey @BOT- run tests", nil) {
		t.Errorf("uppercase body mention of mixed-case localpart must match")
	}
	b := &matrixAdapter{userID: "@Al:matrix.org"}
	if !b.hasMention("@al do it", nil) {
		t.Errorf("lowercase body mention of uppercase localpart must match")
	}
	if got := a.stripMention("@Bot- check the build"); got != "check the build" {
		t.Errorf("stripMention mixed-case = %q, want %q", got, "check the build")
	}
}
