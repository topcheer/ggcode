package swarm

// Regression probes for #3245: the half-open probe slot used to be
// consumed at the GATE (tripped() flipped to HALF_OPEN on the first
// post-cooldown poll). A gate pass that never reached a claim (empty
// board, lost claim race, no task manager) stranded the circuit in
// HALF_OPEN, whose only exit is record() - permanently barring a healthy
// teammate with zero log output. The fix arms the allowance at the gate
// and spends it ONLY after a real claim succeeds (consumeProbe), plus a
// timeout fallback so even a lost probe result cannot strand HALF_OPEN.

import (
	"testing"
	"time"
)

func TestIssue3245_StrandedGateDoesNotConsumeProbe(t *testing.T) {
	resetTeammateBreakers()
	old := teammateBreakerCooldown
	teammateBreakerCooldown = 10 * time.Millisecond
	defer func() { teammateBreakerCooldown = old }()

	id := "t-strand"
	b := teammateBreakerFor(id)
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(15 * time.Millisecond) // cooldown elapses

	// The #3245 stranding scenario: gate passes repeatedly (the board is
	// empty so no claim ever happens). Every pass must stay OPEN - not
	// HALF_OPEN - so nothing is permanently barred.
	for i := 0; i < 5; i++ {
		if !teammateClaimAllowed(id) {
			t.Fatalf("poll %d: expired OPEN must keep releasing the gate, not strand", i)
		}
		if s := b.stateSnapshot(); s != teammateBreakerOpen {
			t.Fatalf("poll %d: gate pass consumed the probe slot (state=%v, want OPEN)", i, s)
		}
	}

	// The moment a REAL claim happens, the slot is spent exactly once.
	if !b.consumeProbe() {
		t.Fatal("consumeProbe must spend the armed slot")
	}
	if s := b.stateSnapshot(); s != teammateBreakerHalfOpen {
		t.Fatalf("after consumeProbe: state=%v, want HALF_OPEN", s)
	}
	if b.consumeProbe() {
		t.Fatal("second consumeProbe must be a no-op (slot already spent)")
	}
}

func TestIssue3245_RealProbeLifecycle(t *testing.T) {
	resetTeammateBreakers()
	old := teammateBreakerCooldown
	teammateBreakerCooldown = 10 * time.Millisecond
	defer func() { teammateBreakerCooldown = old }()

	id := "t-lifecycle"
	b := teammateBreakerFor(id)
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(15 * time.Millisecond)

	// Real claim -> probe spent -> other claims barred until the verdict.
	if !teammateClaimAllowed(id) || !teammateClaimAllowed(id) {
		t.Fatal("expired OPEN must keep passing the gate")
	}
	teammateConsumeProbe(id)
	if teammateClaimAllowed(id) {
		t.Fatal("claims during an in-flight probe must be barred")
	}
	// Probe FAILS -> circuit re-opens with a fresh cooldown (barred now).
	b.record(true)
	if teammateClaimAllowed(id) {
		t.Fatal("re-opened circuit must bar claims during the fresh cooldown")
	}
	// Cooldown elapses again -> allowance re-arms -> probe succeeds -> CLOSED.
	time.Sleep(15 * time.Millisecond)
	if !teammateClaimAllowed(id) {
		t.Fatal("second expiry must re-arm the probe allowance")
	}
	teammateConsumeProbe(id)
	b.record(false)
	if s := b.stateSnapshot(); s != teammateBreakerClosed {
		t.Fatalf("probe success must close: %v", s)
	}
	if !teammateClaimAllowed(id) {
		t.Fatal("closed circuit must allow claims")
	}
}

func TestIssue3245_HalfOpenTimeoutFallback(t *testing.T) {
	resetTeammateBreakers()
	old := teammateBreakerCooldown
	teammateBreakerCooldown = 10 * time.Millisecond
	defer func() { teammateBreakerCooldown = old }()

	id := "t-timeout"
	b := teammateBreakerFor(id)
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(15 * time.Millisecond)
	if !b.consumeProbe() {
		t.Fatal("slot must be spendable after cooldown")
	}
	// The probe's result never arrives (crash/lost path). After a second
	// full cooldown the stranded HALF_OPEN must fall back to OPEN with a
	// fresh timer instead of barring forever. The fallback transition
	// lives inside tripped(), so one gate pass materializes it.
	time.Sleep(25 * time.Millisecond) // > 2x cooldown since probe start
	if teammateClaimAllowed(id) {
		t.Fatal("stranded HALF_OPEN must fall back to a fresh OPEN cooldown (barred)")
	}
	if s := b.stateSnapshot(); s != teammateBreakerOpen {
		t.Fatalf("stranded HALF_OPEN must time out to OPEN, got %v", s)
	}
	time.Sleep(15 * time.Millisecond)
	if !teammateClaimAllowed(id) {
		t.Fatal("fallback OPEN must re-arm after its cooldown")
	}
}

func TestIssue3245_ConsumeProbeGuards(t *testing.T) {
	resetTeammateBreakers()
	id := "t-guards"
	b := teammateBreakerFor(id)
	if b.consumeProbe() {
		t.Fatal("CLOSED circuit: consumeProbe must no-op")
	}
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	if b.consumeProbe() {
		t.Fatal("OPEN before cooldown expiry: consumeProbe must no-op")
	}
	if s := b.stateSnapshot(); s != teammateBreakerOpen {
		t.Fatalf("guard failure mutated state: %v", s)
	}
}
