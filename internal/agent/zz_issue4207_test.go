package agent

import "testing"

// #4207: benign pacing/reassurance phrases ("No rush, take your time") are
// not rejections of the agent's work. The bare "no" in "no biggie"/"no rush"
// must be masked by the whitelist before the rejection scan - same family
// as the #4203 "no problems"/"no sweat" confirmations.
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

// Control: a genuine comma-separated refusal still fires, and the whitelist
// must not blank independent negatives elsewhere on the line.
func TestIssue4207_GenuineRefusalStillFires(t *testing.T) {
	if got := detectNegativeFeedback("No, redo it"); got == "" {
		t.Fatal("genuine refusal 'No, redo it' must still fire")
	}
}
