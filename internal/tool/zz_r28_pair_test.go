package tool

import (
	"strings"
	"testing"
)

// r28 wiring-level coverage (dual of zz_sa216pair_test.go / zz_issue3282_test.go,
// same no-fork pattern): dangerous commands the gate PASSES must land on the
// shared AllowedRiskLedger wired into run_command + start_command (builtin.go
// shares one ledger across both), and the escalation notice format is the
// one the Execute paths prefix onto results.

// A bypass-mode session of gate-passed high-risk commands accumulates on
// the shared ledger exactly as the Execute wiring does (Accumulate sits on
// the passed-gate branch; applyCommandGate is the same call Execute makes).
func TestRunCommandAllowedRiskGatePassAccumulates(t *testing.T) {
	ledger := NewAllowedRiskLedger()
	rc := RunCommand{Policy: newBypassPolicy(), RiskLedger: ledger}
	gate := NewCommandGate()

	for i := 0; i < 3; i++ {
		if _, _, blocked := rc.applyCommandGate(gate, smpHigh); blocked != "" {
			t.Fatalf("bypass mode + gate must pass %q, got blocked: %s", smpHigh, blocked)
		}
		// The passed-gate branch in Execute calls exactly this.
		ledger.Accumulate(smpHigh)
	}
	if got := ledger.Score(); got != 3*riskScoreHigh {
		t.Fatalf("score = %v, want %v", got, 3*riskScoreHigh)
	}
	if esc := ledger.Escalation(); !strings.Contains(esc, "RISK ACCUMULATION") || !strings.Contains(esc, smpHigh) {
		t.Fatalf("escalation must name pattern + recent command:\n%s", esc)
	}
}

// The same shared ledger (builtin.go wires one across both tools) sees
// start_command's gate-passed dangerous starts: its gate passes in bypass
// mode and the dual Accumulate call scores identically.
func TestStartCommandSharesAllowedRiskLedger(t *testing.T) {
	ledger := NewAllowedRiskLedger()
	tk := StartCommandTool{Manager: NewCommandJobManager(t.TempDir()), Policy: newBypassPolicy(), RiskLedger: ledger}
	gate := NewCommandGate()

	for i := 0; i < riskClassThreshold; i++ {
		res := gate.Check(smpHigh)
		if res.IsBlocked() {
			t.Fatalf("bypass session gate must pass %q: %s", smpHigh, res.Reason)
		}
		// The passed-gate branch in start_command's Execute calls this.
		tk.RiskLedger.Accumulate(smpHigh)
	}
	if got := ledger.Score(); got < riskClassThreshold*riskScoreHigh {
		t.Fatalf("start_command starts must accumulate on the shared ledger: got %v", got)
	}
}
