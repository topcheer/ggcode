package agent

import "testing"

// Regression tests for GitHub issue #1176.
//
// The #1162 coordination-marker set included a bare ";". A semicolon is
// ordinary punctuation: it appears inside code snippets (one per Go
// statement), URLs, and plain prose far more often than in explicitly
// sequenced plans. Any turn containing one semicolon got every stated
// intent marked exempt, and exempt intents drain silently at expiry - so
// the detector went from systematic false positives (#1162) to systematic
// false negatives. These tests pin that punctuation alone must not grant
// the multi-intent exemption.
func TestIssue1176_SemicolonInCodeSnippetDoesNotExempt(t *testing.T) {
	s := newReasonActionState()
	// Single stated intent (verify) + a code snippet that contains
	// semicolons. The snippet is not a sequenced multi-step plan, so the
	// intent must still escalate once the tolerance window closes.
	text := "Let me verify the fix works. The loop shape is: for i := 0; i < n; i++ {}"
	for i := 0; i < raAlignmentWindow+2; i++ {
		hint := s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
		_ = hint // escalation timing is the detector's business; silence is not required mid-window
	}
	if s.warnings == 0 {
		t.Fatalf("single verify intent silenced by a semicolon inside a code snippet: 0 warnings after window")
	}
}

func TestIssue1176_SemicolonInProseAndURLDoesNotExempt(t *testing.T) {
	for _, text := range []string{
		"Let me verify the fix; it should pass now.",
		"Let me verify the fix. Reference: https://example.com/a;b",
	} {
		s := newReasonActionState()
		for i := 0; i < raAlignmentWindow+2; i++ {
			s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
		}
		if s.warnings == 0 {
			t.Fatalf("punctuation-only semicolon exempted a single intent: %q", text)
		}
	}
}

func TestIssue1176_GenuineSequencedProseStillExempt(t *testing.T) {
	s := newReasonActionState()
	// A truly sequenced plan ("verify X; then commit") still matches the
	// "then" coordination marker and keeps the #1162 exemption intact.
	text := "Let me verify the fix; then I will commit it."
	for i := 0; i < raAlignmentWindow+2; i++ {
		if hint := s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}}); hint != "" {
			t.Fatalf("sequenced plan with 'then' wrongly escalated on turn %d: %s", i+1, hint)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("sequenced plan produced %d warnings, want 0", s.warnings)
	}
}
