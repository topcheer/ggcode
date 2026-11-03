package swarm

import (
	"testing"
	"time"
)

func TestTeammateBreaker_UnderThresholdReset(t *testing.T) {
	resetTeammateBreakers()
	b := teammateBreakerFor("t-under")
	b.record(true)
	b.record(true)
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("2 failures must not open: %v", b.stateSnapshot())
	}
	b.record(false)
	b.record(true)
	b.record(true)
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("success must reset the streak: %v", b.stateSnapshot())
	}
}

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

	if !teammateClaimAllowed("t-other") {
		t.Fatal("healthy teammate must stay allowed")
	}
}

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
	time.Sleep(15 * time.Millisecond)
	if !teammateClaimAllowed(id) {
		t.Fatal("first claim after cooldown must be released as probe")
	}

	teammateConsumeProbe(id)
	if teammateClaimAllowed(id) {
		t.Fatal("second concurrent claim while HALF-OPEN must be barred")
	}

	b.record(false)
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("probe success must close: %v", b.stateSnapshot())
	}
	if !teammateClaimAllowed(id) {
		t.Fatal("closed circuit must allow claims")
	}
}

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
	if !teammateClaimAllowed(id) {
		t.Fatal("probe must be released after cooldown")
	}
	teammateConsumeProbe(id)
	b.record(true)
	if b.stateSnapshot() != teammateBreakerOpen {
		t.Fatalf("probe failure must re-open: %v", b.stateSnapshot())
	}
	if teammateClaimAllowed(id) {
		t.Fatal("re-opened circuit must bar claims")
	}
}

func TestTeammateBreaker_StragglerSuccessCloses(t *testing.T) {
	resetTeammateBreakers()
	b := teammateBreakerFor("t-straggler")
	for i := 0; i < teammateBreakerThreshold; i++ {
		b.record(true)
	}
	b.record(false)
	if b.stateSnapshot() != teammateBreakerClosed {
		t.Fatalf("straggler success must close: %v", b.stateSnapshot())
	}
}

func TestTeammateBreaker_EmptyIDIgnored(t *testing.T) {
	resetTeammateBreakers()
	if teammateBreakerFor("") != nil {
		t.Fatal("empty ID must yield nil breaker")
	}
	if !teammateClaimAllowed("") {
		t.Fatal("empty ID must always be allowed (inert)")
	}
	recordTeammateTaskResult("", true)
}
