package agent

import "testing"

func TestWordContainsHyphenCompounds(t *testing.T) {
	tests := []struct {
		text string
		pat  string
		want bool
	}{

		{"this is a no-op change", "no", false},
		{"no-op work completed", "no", false},
		{"the stop-gaps help reliability", "stop", false},
		{"bad-API design decision", "bad", false},
		{"patch-set review", "patch", false},
		{"wrap-up of the no-go plan", "no", false},
		{"underscore no_op variable", "no", false},

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

func TestDetectNegativeFeedbackHyphenCompoundNeutral(t *testing.T) {
	for _, msg := range []string{
		"this is a no-op change",
		"the stop-gaps look fine",
	} {
		if cat := detectNegativeFeedback(msg); cat != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (neutral compound)", msg, cat)
		}
	}

	if cat := detectNegativeFeedback("no, wrong approach"); cat == "" {
		t.Error(`detectNegativeFeedback("no, wrong approach") = "", want a category`)
	}
}
