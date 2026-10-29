package agent

import "testing"

// #4033: wordContains must treat hyphens/underscores as word-continuation
// bytes. Bare negative patterns ("no", "stop", "bad") used to match inside
// hyphenated technical compounds ("no-op", "stop-gaps", "bad-API"), firing
// the sentiment escalation ladder on neutral messages.
func TestWordContainsHyphenCompounds(t *testing.T) {
	tests := []struct {
		text string
		pat  string
		want bool
	}{
		// Hyphenated compounds are single tokens: bare word must NOT match.
		{"this is a no-op change", "no", false},
		{"no-op work completed", "no", false},
		{"the stop-gaps help reliability", "stop", false},
		{"bad-API design decision", "bad", false},
		{"patch-set review", "patch", false},
		{"wrap-up of the no-go plan", "no", false},
		{"underscore no_op variable", "no", false},
		// The same word space/punctuation-bounded still matches (control).
		{"no", "no", true},
		{"hello no world", "no", true},
		{"no, redo it", "no", true},
		{"stop", "stop", true},
		{"please stop now", "stop", true},
		{"this is bad", "bad", true},
		{"bad!", "bad", true},
	}
	for _, tt := range tests {
		if got := wordContains(tt.text, tt.pat); got != tt.want {
			t.Errorf("wordContains(%q, %q) = %v, want %v", tt.text, tt.pat, got, tt.want)
		}
	}
}

// #4033 end-to-end: a neutral technical message containing a hyphenated
// compound must not be classified as negative feedback.
func TestDetectNegativeFeedbackHyphenCompoundNeutral(t *testing.T) {
	for _, msg := range []string{
		"this is a no-op change",
		"the stop-gaps look fine",
	} {
		if cat := detectNegativeFeedback(msg); cat != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (neutral compound)", msg, cat)
		}
	}
	// Control: genuine short rejection still detected.
	if cat := detectNegativeFeedback("no, wrong approach"); cat == "" {
		t.Error(`detectNegativeFeedback("no, wrong approach") = "", want a category`)
	}
}
