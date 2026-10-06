package swarm

import (
	"testing"
	"time"
)

// r455: teammate breaker probes (failure-storm bulkhead).

// CLOSED -> 2 failures (under threshold) -> success resets -> still CLOSED.
func TestTeammateBreaker_UnderThresholdReset(t *testing.T) {
	resetTeammateBreakers()
	b := teammateBreakerFor("t-under")
	b.record(true)
	b.record(true)
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("2 failures must not open: %v", b.stateSnapshot())
	}
	b.record(false) // success resets the consecutive counter
	b.record(true)
	b.record(true) // would be 3rd consecutive if not reset
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("success must reset the streak: %v", b.stateSnapshot())
	}
}

// 3 consecutive failures open the circuit; claim gate bars the teammate.
func TestTeammateBreaker_OpensAndBarsClaims(t *testing.T) {
	resetTeammateBreakers()
	id := "t-open"
	for i := 0; i < teammateBreakerThreshold; i++ {
		recordTeammateTaskResult(id, true)
	}
	if b := teammateBreakerFor(id); b.stateSnapshot() != teammateBreakerOpen {
		t.Fatalf("3 consecutive failures must open, got %v", b.stateSnapshot())
	}
	if teammateClaimAllowed(id) {
		t.Fatal("OPEN circuit must bar claims")
	}
	// Another teammate is unaffected (per-teammate isolation).
	if !teammateClaimAllowed("t-other") {
		t.Fatal("healthy teammate must stay allowed")
	}
}

// Cooldown elapses -> HALF-OPEN releases exactly one probe claim.
func TestTeammateBreaker_HalfOpenSingleProbe(t *testing.T) {
	resetTeammateBreakers()
	old := teammateBreakerCooldown
	teammateBreakerCooldown = 10 * time.Millisecond
	defer func() { teammateBreakerCooldown = old }()

	id := "t-probe"
	b := teammateBreakerFor(id)
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(15 * time.Millisecond) // cooldown elapses
	if !teammateClaimAllowed(id) {
		t.Fatal("first claim after cooldown must be released as probe")
	}
	// #3245: the gate only ARMS the allowance; a REAL claim spends it.
	teammateConsumeProbe(id)
	if teammateClaimAllowed(id) {
		t.Fatal("second concurrent claim while HALF-OPEN must be barred")
	}
	// Probe succeeds -> CLOSED, claims flow again.
	b.record(false)
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("probe success must close: %v", b.stateSnapshot())
	}
	if !teammateClaimAllowed(id) {
		t.Fatal("closed circuit must allow claims")
	}
}

// HALF-OPEN probe failure re-opens the circuit.
func TestTeammateBreaker_ProbeFailureReopens(t *testing.T) {
	resetTeammateBreakers()
	old := teammateBreakerCooldown
	teammateBreakerCooldown = time.Millisecond
	defer func() { teammateBreakerCooldown = old }()

	id := "t-reopen"
	b := teammateBreakerFor(id)
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(2 * time.Millisecond)
	if !teammateClaimAllowed(id) { // gate releases the armed allowance
		t.Fatal("probe must be released after cooldown")
	}
	teammateConsumeProbe(id) // #3245: a REAL claim spends the slot
	b.record(true)           // probe fails
	if b.stateSnapshot() != teammateBreakerOpen {
		t.Fatalf("probe failure must re-open: %v", b.stateSnapshot())
	}
	if teammateClaimAllowed(id) {
		t.Fatal("re-opened circuit must bar claims")
	}
}

// A success arriving while OPEN (straggler task) closes the circuit early.
func TestTeammateBreaker_StragglerSuccessCloses(t *testing.T) {
	resetTeammateBreakers()
	b := teammateBreakerFor("t-straggler")
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	b.record(false) // success from a task that started before opening
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("straggler success must close: %v", b.stateSnapshot())
	}
}

// Empty teammate ID is inert (nil breaker paths).
func TestTeammateBreaker_EmptyIDIgnored(t *testing.T) {
	resetTeammateBreakers()
	if teammateBreakerFor("") != nil {
		t.Fatal("empty ID must yield nil breaker")
	}
	if !teammateClaimAllowed("") {
		t.Fatal("empty ID must always be allowed (inert)")
	}
	recordTeammateTaskResult("", true) // must not panic
}
