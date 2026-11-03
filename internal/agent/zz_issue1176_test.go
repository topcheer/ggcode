package agent

import "testing"

func TestIssue1176_SemicolonInCodeSnippetDoesNotExempt(t *testing.T) {
	s := newReasonActionState()

	text := "Let me verify the fix works. The loop shape is: for i := 0; i < n; i++ {}"
	for i := 0; i < raAlignmentWindow+2; i++ {
		hint := s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
		_ = hint
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
