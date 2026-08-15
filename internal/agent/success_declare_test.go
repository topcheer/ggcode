package agent

import (
	"testing"
)

func TestSuccessDeclare_NoDeclaration(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("Let me read the file first.", 0)
	s.recordToolCall()
	s.recordToolCall()
	s.recordToolCall()

	if msg := s.maybeWarn(3); msg != "" {
		t.Errorf("expected no warning without declaration, got: %s", msg)
	}
}

func TestSuccessDeclare_DeclarationButNoActions(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("The task is complete. All done!", 2)

	if msg := s.maybeWarn(3); msg != "" {
		t.Errorf("expected no warning when no actions after declaration, got: %s", msg)
	}
}

func TestSuccessDeclare_OneActionAfterDecl_NoWarn(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("The implementation is complete.", 1)
	s.recordToolCall()

	if msg := s.maybeWarn(3); msg != "" {
		t.Errorf("expected no warning for single action after decl, got: %s", msg)
	}
}

func TestSuccessDeclare_TwoActionsAfterDecl_Warns(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("All set, the fix is ready.", 1)
	s.recordToolCall()
	s.recordToolCall()

	msg := s.maybeWarn(3)
	if msg == "" {
		t.Fatal("expected warning for 2+ actions after declaration")
	}
	if s.declarationIter != 1 {
		t.Errorf("expected declarationIter=1, got %d", s.declarationIter)
	}
}

func TestSuccessDeclare_FiresOnceOnly(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("Everything is working now.", 0)
	s.recordToolCall()
	s.recordToolCall()

	msg1 := s.maybeWarn(2)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	msg2 := s.maybeWarn(3)
	if msg2 != "" {
		t.Errorf("expected no second warning, got: %s", msg2)
	}
}

func TestSuccessDeclare_SameIterationNoWarn(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("The issue is resolved.", 1)
	s.recordToolCall()
	s.recordToolCall()

	// currentIter == declarationIter+1 = 2, should NOT warn (same/cleanup round)
	if msg := s.maybeWarn(2); msg != "" {
		t.Errorf("expected no warning in immediate next iteration, got: %s", msg)
	}
	// currentIter == declarationIter+2 = 3, SHOULD warn
	if msg := s.maybeWarn(3); msg == "" {
		t.Error("expected warning at iteration 3 (2 after declaration)")
	}
}

func TestSuccessDeclare_CaveatSkipsDeclaration(t *testing.T) {
	s := newSuccessDeclareState()
	// "all done" is present but hedged with "still need to"
	s.recordAssistantText("The core logic is all done, but we still need to write tests.", 0)
	s.recordToolCall()
	s.recordToolCall()

	if msg := s.maybeWarn(2); msg != "" {
		t.Errorf("expected no warning for hedged declaration, got: %s", msg)
	}
	if s.declarationIter != -1 {
		t.Errorf("expected declarationIter=-1 for hedged text, got %d", s.declarationIter)
	}
}

func TestSuccessDeclare_OnlyFirstDeclaration(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("All done!", 0)
	s.recordAssistantText("Task is complete again.", 2)

	if s.declarationIter != 0 {
		t.Errorf("expected first declaration at iter 0, got %d", s.declarationIter)
	}
}

func TestSuccessDeclare_Reset(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("All set.", 0)
	s.recordToolCall()
	s.recordToolCall()
	_ = s.maybeWarn(2)

	s.reset()
	if s.declarationIter != -1 || s.fired || s.actionsSince != 0 || s.warnCount != 0 {
		t.Error("reset did not clear state")
	}
}

func TestSuccessDeclare_VariousPhrases(t *testing.T) {
	phrases := []string{
		"we're done here",
		"i'm done with the task",
		"the changes are ready",
		"problem is solved",
		"successfully implemented the feature",
		"finished implementing the solution",
	}
	for _, p := range phrases {
		s := newSuccessDeclareState()
		s.recordAssistantText(p, 0)
		if s.declarationIter != 0 {
			t.Errorf("phrase %q should have triggered declaration", p)
		}
	}
}

func TestSdContainsDeclaration(t *testing.T) {
	cases := []struct {
		text     string
		expected bool
	}{
		{"the task is complete now", true},
		{"All Done!", true},
		{"nothing relevant here", false},
		{"", false},
		{"everything works now as expected", true},
	}
	for _, c := range cases {
		got := sdContainsDeclaration(toLowerSD(c.text))
		if got != c.expected {
			t.Errorf("sdContainsDeclaration(%q) = %v, want %v", c.text, got, c.expected)
		}
	}
}

func TestSdHasCaveat(t *testing.T) {
	cases := []struct {
		text     string
		expected bool
	}{
		{"all done, however there are issues", true},
		{"all done perfectly", false},
		{"the fix is ready, but first we need tests", true},
		{"completely finished", false},
	}
	for _, c := range cases {
		got := sdHasCaveat(toLowerSD(c.text))
		if got != c.expected {
			t.Errorf("sdHasCaveat(%q) = %v, want %v", c.text, got, c.expected)
		}
	}
}

func TestItoaSD(t *testing.T) {
	cases := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{42, "42"},
	}
	for _, c := range cases {
		got := itoaSD(c.input)
		if got != c.expected {
			t.Errorf("itoaSD(%d) = %q, want %q", c.input, got, c.expected)
		}
	}
}

// toLowerSD avoids importing strings in test file redundantly.
func toLowerSD(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// TestSuccessDeclare_WrapUpPhrasingNotVetoed (#352): standard wrap-up
// language elsewhere in the reply must not veto a legitimate declaration.
// Full-text caveat matching previously discarded these and permanently
// blinded the detector (only the first declaration is tracked).
func TestSuccessDeclare_WrapUpPhrasingNotVetoed(t *testing.T) {
	cases := []string{
		"All done. There are no remaining issues.",                                              // "remaining" self-veto
		"Everything is working. Note that I also refactored the helper.",                        // "note that"
		"The task is complete. However, see the docs for details.",                              // "however"
		"All done. For reference, a sensible next step for you would be deploying it yourself.", // "next step" beyond trailing window
	}
	for _, text := range cases {
		s := newSuccessDeclareState()
		s.recordAssistantText(text, 0)
		if s.declarationIter != 0 {
			t.Errorf("wrap-up phrasing must not veto declaration %q: declarationIter=%d", text, s.declarationIter)
		}
	}
}

// TestSuccessDeclare_NearbyCaveatStillVetoes (#352): hedging immediately
// adjacent to the claim still vetoes it — the window is local, not removed.
func TestSuccessDeclare_NearbyCaveatStillVetoes(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("All done, but first we need to run the tests.", 0)
	if s.declarationIter != -1 {
		t.Errorf("adjacent hedge must still veto: declarationIter=%d", s.declarationIter)
	}
}

// TestSuccessDeclare_ExpandedPhrases (#352): common declaration variants
// previously missed by the phrase table.
func TestSuccessDeclare_ExpandedPhrases(t *testing.T) {
	cases := []struct {
		text    string
		wantRec bool
	}{
		{"Task complete.", true},
		{"Done!", true},
		{"The fix works.", true},
		{"Please review the wall setup drawing.", false}, // "all set" substring must not match
	}
	for _, c := range cases {
		s := newSuccessDeclareState()
		s.recordAssistantText(c.text, 0)
		got := s.declarationIter == 0
		if got != c.wantRec {
			t.Errorf("recordAssistantText(%q) recorded=%v, want %v", c.text, got, c.wantRec)
		}
	}
}
