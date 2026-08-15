package agent

// Characterization tests for spiral_hallucin.go bug verification.
//
// Sub-problem A: spiralMinGap=2 is documented ("minimum turns between
// uncertainty and commitment... Same-turn would just be inconsistency")
// but never enforced. These tests prove committed counting starts at
// gap=0 (same turn) and gap=1.
//
// Sub-problem B: a global `verified` flag is reset by ANY new uncertainty
// about ANY topic, so a topic that was genuinely verified loses its
// protection when the agent hedges about something unrelated later.

import (
	"strings"
	"testing"
)

// --- Sub-problem A: gap semantics never enforced ---

// Same-turn: uncertainty marker and committed language about the same
// topic in ONE text, with the topic >100 chars away from the uncertainty
// marker (so the stillHedging guard misses it). spiralMinGap=2 says this
// "would just be inconsistency" and must NOT count.
func TestSpiralBugVerify_SameTurnCommitCounts(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	sameTurn := "I assume the configuration defaults in this repository were " +
		"carefully reviewed and vetted by the maintainers over many release " +
		"cycles, so the postgres settings inherit safe values."
	a.recordSpiralTurn(sameTurn)

	if got := s_counts(a)["postgres"]; got != 1 {
		t.Fatalf("sub-problem A (same-turn): expected committedCounts[postgres]=1 "+
			"on the SAME turn as the uncertainty (gap=0), got %d — proves "+
			"spiralMinGap=2 semantics are not enforced", got)
	}
	// One more committed turn at gap=1 crosses spiralMinCommittedTurns=2.
	a.recordSpiralTurn("Because the postgres settings inherit safe values, we can proceed.")
	w := a.maybeWarnSpiralHallucination()
	if w == "" {
		t.Fatal("sub-problem A: expected warning to fire with max gap=1")
	}
	if !strings.Contains(w, "postgres") {
		t.Fatalf("warning should list postgres, got: %s", w)
	}
}

// Gap=1 only: pure uncertainty turn followed immediately by committed turns.
// Documented spiralMinGap=2 requires >=2 turns of distance.
func TestSpiralBugVerify_Gap1Counts(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("sanity: turn 1 must not count, got %d", got)
	}
	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	if got := s_counts(a)["postgres"]; got != 1 {
		t.Fatalf("sub-problem A (gap=1): committedCounts[postgres]=%d, want 1 "+
			"(spiralMinGap=2 should have suppressed counting until gap>=2)", got)
	}
	a.recordSpiralTurn("Now that the postgres port is settled, moving on.")
	w := a.maybeWarnSpiralHallucination()
	if w == "" {
		t.Fatal("sub-problem A: warning fired with all committed mentions at gap<2")
	}
}

// --- Sub-problem B: unrelated new uncertainty resets global verified ---

// Turn 1: uncertainty about "postgres". Verification succeeds (execution
// tool). Turn 2: agent hedges about an UNRELATED topic ("retry logic") —
// global verified is wiped. Turns 3-4: committed mentions of the ALREADY
// VERIFIED postgres topic accumulate and fire the warning.
func TestSpiralBugVerify_VerifiedTopicLosesProtection(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// Turn 1: assume postgres port.
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	if a.spiralState.verified {
		t.Fatal("sanity: verified must be false after uncertainty")
	}
	// Successful execution tool run (agent.go:4238 gate) verifies it.
	a.recordSpiralVerification("run_command")
	if !a.spiralState.verified {
		t.Fatal("sanity: run_command success must set verified=true")
	}

	// Turn 2: unrelated hedging — "I think the retry logic is safer with backoff".
	// Must NOT contain committed language about postgres (it doesn't).
	a.recordSpiralTurn("I think the retry logic is safer with exponential backoff here.")
	if a.spiralState.verified {
		t.Fatal("sub-problem B: verified still true — unrelated uncertainty should NOT reset it")
	}

	// Turns 3-4: committed assertions about the VERIFIED postgres topic.
	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	a.recordSpiralTurn("Now that the postgres port is settled, the deploy can continue.")

	w := a.maybeWarnSpiralHallucination()
	if w == "" {
		t.Fatal("sub-problem B: expected warning despite topic being verified")
	}
	if !strings.Contains(w, "postgres") {
		t.Fatalf("warning lists the verified topic: %s", w)
	}
}

// Control: identical sequence WITHOUT the turn-2 unrelated uncertainty —
// verified stays true, single spiraled topic < 3, no warning. Proves the
// reset (not the committed mentions) is what breaks the protection.
func TestSpiralBugVerify_Control_NoUnrelatedUncertainty(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	a.recordSpiralVerification("run_command")
	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	a.recordSpiralTurn("Now that the postgres port is settled, the deploy can continue.")
	if w := a.maybeWarnSpiralHallucination(); w != "" {
		t.Fatalf("control: warning should stay silent when verified holds, got: %s", w)
	}
}

func s_counts(a *Agent) map[string]int {
	if a.spiralState == nil {
		t := make(map[string]int)
		return t
	}
	return a.spiralState.committedCounts
}
