package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func co2675(t *testing.T, branch string) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"branch": branch})
	return b
}

func TestIssue2675_CheckoutRoundtripStructure(t *testing.T) {

	s := newActionAnnihilateState()
	if w := s.recordToolCall("git_checkout", co2675(t, "main"), 1); w != "" {
		t.Errorf("first checkout warned: %s", w)
	}
	s.recordToolCall("run_command", json.RawMessage(`{"command":"git status"}`), 2)
	if w := s.recordToolCall("git_checkout", co2675(t, "main"), 3); w != "" {
		t.Errorf("idempotent re-checkout A→tools→A warned (scenario B #2675): %s", w)
	}

	s2 := newActionAnnihilateState()
	s2.recordToolCall("git_checkout", co2675(t, "main"), 1)

	if w := s2.recordToolCall("git_checkout", co2675(t, "main"), 3); w != "" {
		t.Errorf("A→failed-B→A warned despite no successful B leg: %s", w)
	}

	s3 := newActionAnnihilateState()
	s3.recordToolCall("git_checkout", co2675(t, "main"), 1)
	s3.recordToolCall("git_checkout", co2675(t, "feature"), 2)
	w := s3.recordToolCall("git_checkout", co2675(t, "main"), 3)
	if w == "" || !strings.Contains(w, "thrashing") {
		t.Errorf("true A→B→A roundtrip must warn (got %q)", w)
	}
}
