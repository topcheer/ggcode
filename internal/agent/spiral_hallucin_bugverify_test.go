package agent

// Post-fix regression tests for spiral_hallucin.go (#493), flipping the
// pre-fix characterization suite (commit 4bb20bdd).
//
// Sub-problem A: spiralMinGap=2 is documented ("minimum turns between
// uncertainty and commitment... Same-turn would just be inconsistency")
// and NOW ENFORCED — committed counting only starts at gap>=2.
//
// Sub-problem B: verified is PER-TOPIC — a genuinely verified topic keeps
// its protection when the agent later hedges about something unrelated.

import (
	"strings"
	"testing"
)

// --- Sub-problem A: gap semantics now enforced (#493 fix) ---

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

	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("post-fix (#493 A): same-turn commitment (gap=0) must NOT count, got %d", got)
	}
}

// Gap<spiralMinGap commitments are suppressed; a genuine spiral with
// commitments at gap>=2 still accumulates and triggers.
func TestSpiralBugVerify_Gap1Counts(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("sanity: turn 1 must not count, got %d", got)
	}
	// gap=1: still below spiralMinGap=2 — suppressed.
	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("post-fix (#493 A): gap=1 commitment must NOT count, got %d", got)
	}
	// gap=2 and gap=3: genuine spiral territory — counts accumulate.
	a.recordSpiralTurn("Now that the postgres port is settled, moving on.")
	a.recordSpiralTurn("Therefore the postgres port stays pinned in the deploy config.")
	if got := s_counts(a)["postgres"]; got != 2 {
		t.Fatalf("gap>=2 commitments must count, got %d", got)
	}
	w := a.maybeWarnSpiralHallucination()
	if w == "" {
		t.Fatal("genuine spiral (2 committed turns at gap>=2, unverified) must still warn")
	}
	if !strings.Contains(w, "postgres") {
		t.Fatalf("warning should list postgres, got: %s", w)
	}
}

// --- Sub-problem B: per-topic verified protection (#493 fix) ---

// Turn 1: uncertainty about "postgres". Verification succeeds (execution
// tool). Turn 2: agent hedges about an UNRELATED topic ("retry logic") —
// with per-topic state the postgres topic KEEPS its verified protection.
// Turns 3-4: committed mentions of the verified topic stay uncounted and
// the warning stays silent.
func TestSpiralBugVerify_VerifiedTopicLosesProtection(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// Turn 1: assume postgres port.
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	if len(a.spiralState.topics) == 0 {
		t.Fatal("sanity: topic must be tracked")
	}
	if a.spiralState.topics[0].verified {
		t.Fatal("sanity: topic starts unverified")
	}
	// Successful execution tool run (agent.go tool-loop gate) verifies it.
	a.recordSpiralVerification("run_command")
	if !a.spiralState.topics[0].verified {
		t.Fatal("sanity: run_command success must mark the topic verified")
	}

	// Turn 2: unrelated hedging — must NOT strip postgres's protection.
	a.recordSpiralTurn("I think the retry logic is safer with exponential backoff here.")
	if !a.spiralState.topics[0].verified {
		t.Fatal("post-fix (#493 B): unrelated uncertainty must NOT reset the verified topic")
	}

	// Turns 3-4: committed assertions about the VERIFIED postgres topic.
	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	a.recordSpiralTurn("Now that the postgres port is settled, the deploy can continue.")

	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("post-fix (#493 B): verified topic must not accumulate committed counts, got %d", got)
	}
	if w := a.maybeWarnSpiralHallucination(); w != "" {
		t.Fatalf("post-fix (#493 B): warning must stay silent for a verified topic, got: %s", w)
	}
}

// Control: identical sequence WITHOUT the verification — the unverified
// topic spirals and DOES warn. Proves the fix silenced the false positive
// without silencing the true positive.
func TestSpiralBugVerify_Control_NoUnrelatedUncertainty(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	// NO verification between.
	a.recordSpiralTurn("The migration prepares the schema for the deployment first.")     // gap=1, suppressed
	a.recordSpiralTurn("Now that the postgres port is settled, the deploy can continue.") // gap=2, counts
	a.recordSpiralTurn("Therefore the postgres port stays pinned in the deploy config.")  // gap=3, counts
	w := a.maybeWarnSpiralHallucination()
	if w == "" {
		t.Fatal("control: unverified spiral must still warn")
	}
}

func s_counts(a *Agent) map[string]int {
	if a.spiralState == nil {
		t := make(map[string]int)
		return t
	}
	return a.spiralState.committedCounts
}
