package tool

import (
	"strings"
	"testing"
)

func TestRunCommandAllowedRiskGatePassAccumulates(t *testing.T) {
	ledger := NewAllowedRiskLedger()
	rc := RunCommand{Policy: newBypassPolicy(), RiskLedger: ledger}
	gate := NewCommandGate()

	for i := 0; i < 3; i++ {
		if _, _, blocked := rc.applyCommandGate(gate, smpHigh); blocked != "" {
			t.Fatalf("bypass mode + gate must pass %q, got blocked: %s", smpHigh, blocked)
		}

		ledger.Accumulate(smpHigh)
	}
	if got := ledger.Score(); got != 3*riskScoreHigh {
		t.Fatalf("score = %v, want %v", got, 3*riskScoreHigh)
	}
	if esc := ledger.Escalation(); !strings.Contains(esc, "RISK ACCUMULATION") || !strings.Contains(esc, smpHigh) {
		t.Fatalf("escalation must name pattern + recent command:\n%s", esc)
	}
}

func TestStartCommandSharesAllowedRiskLedger(t *testing.T) {
	ledger := NewAllowedRiskLedger()
	tk := StartCommandTool{Manager: NewCommandJobManager(t.TempDir()), Policy: newBypassPolicy(), RiskLedger: ledger}
	gate := NewCommandGate()

	for i := 0; i < riskClassThreshold; i++ {
		res := gate.Check(smpHigh)
		if res.IsBlocked() {
			t.Fatalf("bypass session gate must pass %q: %s", smpHigh, res.Reason)
		}

		tk.RiskLedger.Accumulate(smpHigh)
	}
	if got := ledger.Score(); got < riskClassThreshold*riskScoreHigh {
		t.Fatalf("start_command starts must accumulate on the shared ledger: got %v", got)
	}
}
