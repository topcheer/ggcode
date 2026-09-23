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

// #2675: matchCheckoutRoundtrip's pairwise prior==current matcher fired on
// structures that are not roundtrips:
//   - Scenario B: idempotent re-checkout (A → other tools → A) - no B leg,
//     nothing was undone;
//   - Scenario A (state-level half): a failed A attempt leaves no side
//     effect to come back FROM, so even A → B(failed) → A switches nowhere.
//
// A true roundtrip A→B→A must show a checkout to a DIFFERENT branch between
// the matching prior and the current call.
func TestIssue2675_CheckoutRoundtripStructure(t *testing.T) {
	// Idempotent re-checkout: A → status → A must NOT warn.
	s := newActionAnnihilateState()
	if w := s.recordToolCall("git_checkout", co2675(t, "main"), 1); w != "" {
		t.Errorf("first checkout warned: %s", w)
	}
	s.recordToolCall("run_command", json.RawMessage(`{"command":"git status"}`), 2)
	if w := s.recordToolCall("git_checkout", co2675(t, "main"), 3); w != "" {
		t.Errorf("idempotent re-checkout A→tools→A warned (scenario B #2675): %s", w)
	}

	// Failed-B variant: A → B(failed, not recorded post-fix) → A must NOT warn.
	s2 := newActionAnnihilateState()
	s2.recordToolCall("git_checkout", co2675(t, "main"), 1)
	// failed checkout to feature would not be recorded at the wiring level
	// (#2675 IsError gate); simulate by not recording it.
	if w := s2.recordToolCall("git_checkout", co2675(t, "main"), 3); w != "" {
		t.Errorf("A→failed-B→A warned despite no successful B leg: %s", w)
	}

	// TRUE roundtrip A→B→A must still warn (regression guard).
	s3 := newActionAnnihilateState()
	s3.recordToolCall("git_checkout", co2675(t, "main"), 1)
	s3.recordToolCall("git_checkout", co2675(t, "feature"), 2)
	w := s3.recordToolCall("git_checkout", co2675(t, "main"), 3)
	if w == "" || !strings.Contains(w, "thrashing") {
		t.Errorf("true A→B→A roundtrip must warn (got %q)", w)
	}
}
