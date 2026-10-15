package agent

// #4174 regression tests: sentiment opener window must use the word-BOUNDED
// index (wordIndexIn), not bare strings.Index. The gate is wordContains
// (word-bounded); a bare substring index could hit a non-word embedding at
// the message start ("Now"/"Nobody"/"Note"/"Know" embed "no") while the gate
// matched a standalone negative word MID-message - the opener window then
// borrowed the embedding's tiny prefix position and misclassified a neutral
// long message as negative feedback.

import "testing"

func TestIssue4174OpenerWindowNotBorrowedByEmbeddedPrefix(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{
			name: "Now-prefix embedding with mid-message standalone no",
			message: "Now that we've validated the parser, let's discuss the deployment plan. " +
				"I reviewed the checklist with the team yesterday and we agreed the answer is no " +
				"for now, because the staging cluster is not ready yet and we don't want to risk it.",
		},
		{
			name: "Nobody-prefix embedding with trailing standalone wrong",
			message: "Nobody has reviewed the migration notes so far, so let's schedule a session tomorrow " +
				"and walk through the open questions one by one until we agree the plan is right, " +
				"because several details still look wrong to me.",
		},
		{
			name: "Note-prefix embedding with mid-message standalone wrong",
			message: "Note that some tests still fail on the CI runner, the flaky ones timeout intermittently, " +
				"and the second failure is definitely wrong because the fixture was never regenerated.",
		},
		{
			name: "Knowledge-prefix control (never triggered, stays clean)",
			message: "Knowledge of the retry policy helps here: the provider retries transient 429s internally, " +
				"so an occasional bad response is expected and simply logged rather than escalated.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectNegativeFeedback(tc.message); got != "" {
				t.Errorf("neutral long message misclassified as negative feedback: category=%q\nmessage: %.80s...", got, tc.message)
			}
			// The escalation counter must not advance on such messages either.
			s := newUserSentimentState()
			if fb := s.analyzeAndUpdate(tc.message); fb.Level != 0 {
				t.Errorf("escalation level advanced on neutral message: level=%d", fb.Level)
			}
		})
	}
}

func TestIssue4174GenuineOpenerStillFires(t *testing.T) {
	// A long message that BEGINS with a genuine standalone negative word must
	// still take the opener path (#4126 behavior preserved by the fix).
	long := "wrong approach entirely; we need to step back and reconsider the whole design " +
		"from first principles, including the data model and the API surface, before writing more code."
	if got := detectNegativeFeedback(long); got != negCatRejection {
		t.Errorf("genuine rejection opener on long message not detected: got %q, want %q", got, negCatRejection)
	}

	// Redirection opener keeps its 40-rune window semantics (#1521).
	longRedirect := "Actually I meant for us to reuse the existing helper instead of writing a new one, " +
		"since the helper already handles all the edge cases we care about here."
	if got := detectNegativeFeedback(longRedirect); got != negCatRedirection {
		t.Errorf("genuine redirection opener on long message not detected: got %q, want %q", got, negCatRedirection)
	}

	// A negative word PAST the 12-rune preamble on a non-redirection long
	// message stays context, not feedback (pre-existing #4126 semantics).
	past := "I was thinking that this wrong approach might actually be defensible if we reconsider " +
		"the constraints, but for now let's keep looking at alternatives together."
	if got := detectNegativeFeedback(past); got != "" {
		t.Errorf("mid-sentence negative word past opener window fired: got %q", got)
	}
}

func TestIssue4174EscalationNotPoisoned(t *testing.T) {
	// Two consecutive neutral "Now that..." messages must not climb the
	// escalation ladder (pre-fix bug: each borrowed hit counted as level-1
	// feedback, and two in a row reset monitoring state).
	s := newUserSentimentState()
	m1 := "Now that we've validated the parser, let's discuss the deployment plan. The team agreed " +
		"the answer is no for now because the staging cluster is not ready yet for a Friday release."
	m2 := "Note that some tests still fail on the runner, and the second failure is definitely wrong " +
		"because the fixture was never regenerated after the schema change landed last week."
	if fb := s.analyzeAndUpdate(m1); fb.Level != 0 {
		t.Fatalf("first neutral message escalated: level=%d", fb.Level)
	}
	if fb := s.analyzeAndUpdate(m2); fb.Level != 0 {
		t.Fatalf("second neutral message escalated: level=%d", fb.Level)
	}
}
