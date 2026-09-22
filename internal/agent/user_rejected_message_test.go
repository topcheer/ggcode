package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUserRejectedMessageCarriesPatternAndAntiRetry pins the denial
// feedback contract: the classifier prefix stays intact, the generalized
// remembered pattern is surfaced, and the anti-retry steering guidance is
// present so the model does not burn turns re-prompting the user.
func TestUserRejectedMessageCarriesPatternAndAntiRetry(t *testing.T) {
	msg := userRejectedMessage("run_command", json.RawMessage(`{"command":"git push origin main"}`))

	// effect_ledger / mutating ledger classifiers match this prefix.
	if !strings.HasPrefix(msg, `Permission denied for tool "run_command".`) {
		t.Errorf("classifier prefix broken: %q", msg)
	}
	if !strings.Contains(msg, "remembered pattern: run_command:git push") {
		t.Errorf("missing remembered pattern key: %q", msg)
	}
	if !strings.Contains(msg, "Do not retry the identical call") {
		t.Errorf("missing anti-retry guidance: %q", msg)
	}
}

func TestUserRejectedMessageChainedCommandNoAutoApproveKey(t *testing.T) {
	// Chained commands never collapse to a narrow key (#777) — the full
	// command (plus the no-auto-approve marker) must appear in the feedback.
	msg := userRejectedMessage("run_command", json.RawMessage(`{"command":"make build && make test"}`))
	if !strings.Contains(msg, "no-auto-approve:chained") {
		t.Errorf("chained command should widen the remembered key: %q", msg)
	}
}
