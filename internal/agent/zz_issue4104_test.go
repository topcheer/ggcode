package agent

import "testing"

func TestIssue4104BareStopNotImperativeMisfire(t *testing.T) {

	for _, msg := range []string{
		"stop the dev server and check the logs",
		"stop the cron job",
		"stop the tests for now",
	} {
		if cat := detectNegativeFeedback(msg); cat == "frustration" {
			t.Errorf("imperative ops command misclassified as frustration: %q", msg)
		}
	}

	for _, msg := range []string{"stop", "stop!", "STOP.", " stop "} {
		if cat := detectNegativeFeedback(msg); cat != "frustration" {
			t.Errorf("bare abort command must stay frustration: %q got %q", msg, cat)
		}
	}

	if cat := detectNegativeFeedback("stop doing that"); cat != "frustration" {
		t.Errorf("stop doing must stay frustration, got %q", cat)
	}
}
