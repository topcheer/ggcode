package agent

import (
	"strings"
	"testing"
)

func TestDelegatedStopIsDrainOnlyPathOnceFlagBlocksMainPath(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1000)

	a.RecordDelegatedTokenUsage(950, 0)
	a.RecordDelegatedTokenUsage(51, 0)
	msg, stop := a.SessionTokenBudgetAlert()
	if msg == "" || !stop {
		t.Fatalf("delegated crossing 100%% should drain (msg, stop=true), got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "exhausted") {
		t.Fatalf("stop alert should say exhausted, got %q", msg)
	}

	mainMsg, mainStop := a.RecordSessionTokenUsage(500, 0)
	if mainMsg != "" || mainStop {
		t.Fatalf("main path after delegated stop must return (\"\", false) -- it is not the armed path; got msg=%q stop=%v", mainMsg, mainStop)
	}

}

func TestDelegatedLadderAlertsDrainAcrossIterations(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1000)

	a.RecordDelegatedTokenUsage(960, 0)
	msg, stop := a.SessionTokenBudgetAlert()
	if !strings.Contains(msg, "95%") || stop {
		t.Fatalf("first drain should be the 95%% finalize alert, got msg=%q stop=%v", msg, stop)
	}

	a.RecordDelegatedTokenUsage(41, 0)
	msg, stop = a.SessionTokenBudgetAlert()
	if !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("second drain should carry stop=true, got msg=%q stop=%v", msg, stop)
	}
}
