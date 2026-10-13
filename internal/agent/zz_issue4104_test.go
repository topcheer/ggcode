package agent

import "testing"

// #4104: bare "stop" in the frustration group fired on imperative ops
// commands and injected a misleading "your approach was unwanted" signal
// (two in a row reset monitoring). Bare stop now hits only when the
// message is essentially just "stop"; frustrated-work stop stays covered
// by "stop doing"/"stop trying".
func TestIssue4104BareStopNotImperativeMisfire(t *testing.T) {
	// Imperative ops commands: NOT frustration anymore.
	for _, msg := range []string{
		"stop the dev server and check the logs",
		"stop the cron job",
		"stop the tests for now",
	} {
		if cat := detectNegativeFeedback(msg); cat == "frustration" {
			t.Errorf("imperative ops command misclassified as frustration: %q", msg)
		}
	}
	// Genuine abort: bare stop (optionally punctuated) still fires.
	for _, msg := range []string{"stop", "stop!", "STOP.", " stop "} {
		if cat := detectNegativeFeedback(msg); cat != "frustration" {
			t.Errorf("bare abort command must stay frustration: %q got %q", msg, cat)
		}
	}
	// Frustrated-work stop via the compound patterns.
	if cat := detectNegativeFeedback("stop doing that"); cat != "frustration" {
		t.Errorf("stop doing must stay frustration, got %q", cat)
	}
}
