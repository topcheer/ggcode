package agent

import "testing"

// #4033: ASCII hyphen is an in-word character for wordContains/isAlnum, so
// bare components of engineering compounds (no-op, bad-API, stop-gaps,
// redo-log) must NOT hit negative sentiment patterns as standalone words.
// The issue's trigger: two consecutive neutral compound-mentioning short
// messages escalated to strong (monitoring reset + misleading STOP guidance).
func TestIssue4033HyphenCompoundsDoNotHitNegativePatterns(t *testing.T) {
	neutral := []string{
		"no-op change is fine",
		"this is a no-op",
		"bad-API design is acceptable for now",
		"stop-gaps remain before the release",
		"re-run the tests after the no-op fix", // prefix compound too
	}
	for _, msg := range neutral {
		s := newUserSentimentState()
		if fb := s.analyzeAndUpdate(msg); fb.Level != 0 {
			t.Errorf("analyzeAndUpdate(%q) level=%d, want 0 (compound component must not hit)", msg, fb.Level)
		}
	}
}

// Guard: genuine negative feedback keeps working (plain standalone words,
// hyphen only shields the compound-internal positions).
func TestIssue4033PlainNegativesStillDetected(t *testing.T) {
	negative := []string{
		"no, that's wrong",
		"this is bad, stop",
		"stop doing that",
		"no. do it again differently",
	}
	for _, msg := range negative {
		s := newUserSentimentState()
		if fb := s.analyzeAndUpdate(msg); fb.Level == 0 {
			t.Errorf("analyzeAndUpdate(%q) level=0, want detected", msg)
		}
	}
}

// Unit-level pin of the boundary semantics change itself.
func TestIssue4033WordContainsHyphenBoundary(t *testing.T) {
	// Hyphen now bonds words: "no" inside "no-op" is NOT standalone.
	if wordContains("no-op change", "no") {
		t.Error(`wordContains("no-op change", "no") = true, want false (hyphen is in-word)`)
	}
	if wordContains("bad-API design", "bad") {
		t.Error(`wordContains("bad-API design", "bad") = true, want false`)
	}
	// But standalone "no" still matches with real boundaries.
	if !wordContains("no, that's wrong", "no") {
		t.Error(`wordContains("no, that's wrong", "no") = false, want true`)
	}
	// Hyphen-adjacent at string boundary: "no-" (trailing hyphen) still bonds.
	if wordContains("this is a no-", "no") {
		t.Error(`wordContains("this is a no-", "no") = true, want false`)
	}
	// CJK degeneration unaffected (bytes >= 0x80 are boundaries).
	if !wordContains("不要 no 再改了", "no") {
		t.Error("standalone ASCII word between CJK must still match (CJK bytes are boundaries)")
	}
	// Apostrophe semantics preserved (regression guard from pre-#4033).
	if wordContains("don't do that", "no") {
		t.Error(`wordContains("don't do that", "no") = true, want false (apostrophe bonds)`)
	}
}
