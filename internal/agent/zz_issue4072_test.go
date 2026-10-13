package agent

import (
	"strings"
	"testing"
)

// #4072: delegated spend crossing 100% first parks (msg, stop=true) via
// RecordDelegatedTokenUsage, but the once-flag (stopGiven) it sets makes the
// MAIN path RecordSessionTokenUsage return ("", false) for the rest of the
// run -- the loop's drain block (SessionTokenBudgetAlert) is therefore the
// ONLY path that can arm the sessionBudgetStop gate. This test pins that
// chain so a regression on either leg (main-path fallback or drainable
// stop=true) cannot silently re-create the never-stopping run.
func TestDelegatedStopIsDrainOnlyPathOnceFlagBlocksMainPath(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1000)

	// Leg 1: delegated spend walks the ladder to 100% and parks stop=true.
	a.RecordDelegatedTokenUsage(950, 0) // 95%: finalize alert parked
	a.RecordDelegatedTokenUsage(51, 0)  // 100.1%: stop alert parked (once-flag now true)
	msg, stop := a.SessionTokenBudgetAlert()
	if msg == "" || !stop {
		t.Fatalf("delegated crossing 100%% should drain (msg, stop=true), got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "exhausted") {
		t.Fatalf("stop alert should say exhausted, got %q", msg)
	}

	// Leg 2: the main path can no longer report stop -- the once-flag blocks
	// it even as the main loop keeps burning tokens past 100%.
	mainMsg, mainStop := a.RecordSessionTokenUsage(500, 0)
	if mainMsg != "" || mainStop {
		t.Fatalf("main path after delegated stop must return (\"\", false) -- it is not the armed path; got msg=%q stop=%v", mainMsg, mainStop)
	}

	// Consequently the drain block's stop=true in the agent loop is the only
	// remaining chance to arm sessionBudgetStop; the fix wires it (agent.go,
	// delegated drain block, parity with token/cost/time twins).
}

// Variant: the 95% finalize alert and the 100% stop alert must BOTH be
// drainable across separate drain calls -- the loop drains once per iteration,
// so parking two alerts into one pendingMsg slot must not swallow the stop.
func TestDelegatedLadderAlertsDrainAcrossIterations(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1000)

	// Single delegated crossing parks the 95% finalize alert (stop=false):
	// checkLocked returns the FIRST matched rung, so the stop alert for the
	// same crossing is NOT lost -- the next delegated spend parks it.
	a.RecordDelegatedTokenUsage(960, 0) // 96%
	msg, stop := a.SessionTokenBudgetAlert()
	if !strings.Contains(msg, "95%") || stop {
		t.Fatalf("first drain should be the 95%% finalize alert, got msg=%q stop=%v", msg, stop)
	}
	// Next iteration's delegated spend parks the hard stop.
	a.RecordDelegatedTokenUsage(41, 0) // 100.1%
	msg, stop = a.SessionTokenBudgetAlert()
	if !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("second drain should carry stop=true, got msg=%q stop=%v", msg, stop)
	}
}
