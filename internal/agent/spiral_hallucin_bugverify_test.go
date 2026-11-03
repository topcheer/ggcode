package agent

import (
	"strings"
	"testing"
)

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

func TestSpiralBugVerify_Gap1Counts(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("sanity: turn 1 must not count, got %d", got)
	}

	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("post-fix (#493 A): gap=1 commitment must NOT count, got %d", got)
	}

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

func TestSpiralBugVerify_VerifiedTopicLosesProtection(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")
	if len(a.spiralState.topics) == 0 {
		t.Fatal("sanity: topic must be tracked")
	}
	if a.spiralState.topics[0].verified {
		t.Fatal("sanity: topic starts unverified")
	}

	a.recordSpiralVerification("run_command", "exit 0")
	if !a.spiralState.topics[0].verified {
		t.Fatal("sanity: run_command success must mark the topic verified")
	}

	a.recordSpiralTurn("I think the retry logic is safer with exponential backoff here.")
	if !a.spiralState.topics[0].verified {
		t.Fatal("post-fix (#493 B): unrelated uncertainty must NOT reset the verified topic")
	}

	a.recordSpiralTurn("Because the postgres port is correct, the migration will proceed.")
	a.recordSpiralTurn("Now that the postgres port is settled, the deploy can continue.")

	if got := s_counts(a)["postgres"]; got != 0 {
		t.Fatalf("post-fix (#493 B): verified topic must not accumulate committed counts, got %d", got)
	}
	if w := a.maybeWarnSpiralHallucination(); w != "" {
		t.Fatalf("post-fix (#493 B): warning must stay silent for a verified topic, got: %s", w)
	}
}

func TestSpiralBugVerify_Control_NoUnrelatedUncertainty(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the postgres port is 5432 for this deployment.")

	a.recordSpiralTurn("The migration prepares the schema for the deployment first.")
	a.recordSpiralTurn("Now that the postgres port is settled, the deploy can continue.")
	a.recordSpiralTurn("Therefore the postgres port stays pinned in the deploy config.")
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
