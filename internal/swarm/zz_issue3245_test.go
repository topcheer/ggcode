package swarm

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
	time.Sleep(15 * time.Millisecond)

	for i := 0; i < 5; i++ {
		if !teammateClaimAllowed(id) {
			t.Fatalf("poll %d: expired OPEN must keep releasing the gate, not strand", i)
		}
		if s := b.stateSnapshot(); s != teammateBreakerOpen {
			t.Fatalf("poll %d: gate pass consumed the probe slot (state=%v, want OPEN)", i, s)
		}
	}

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

	if !teammateClaimAllowed(id) || !teammateClaimAllowed(id) {
		t.Fatal("expired OPEN must keep passing the gate")
	}
	teammateConsumeProbe(id)
	if teammateClaimAllowed(id) {
		t.Fatal("claims during an in-flight probe must be barred")
	}

	b.record(true)
	if teammateClaimAllowed(id) {
		t.Fatal("re-opened circuit must bar claims during the fresh cooldown")
	}

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

	time.Sleep(25 * time.Millisecond)
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
