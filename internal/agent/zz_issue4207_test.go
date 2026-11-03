package agent

import "testing"

func TestIssue4207_BenignNoPhrasesNotRejection(t *testing.T) {
	benign := []string{
		"No rush, take your time",
		"No biggie, go ahead",
		"No biggie, looks fine",
		"no rush, whenever you're ready",
	}
	for _, msg := range benign {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (benign)", msg, got)
		}
	}
}

func TestIssue4207_GenuineRefusalStillFires(t *testing.T) {
	if got := detectNegativeFeedback("No, redo it"); got == "" {
		t.Fatal("genuine refusal 'No, redo it' must still fire")
	}
}
