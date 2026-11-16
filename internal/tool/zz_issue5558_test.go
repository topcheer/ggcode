package tool

import (
	"strings"
	"testing"
	"time"
)

// #5558 Residual 2: dual-dimension budgets - per-pair cooldown and
// per-sender rolling window each name themselves and carry their own countdown.

func TestIssue5558_SenderBudgetDimension(t *testing.T) {
	r := newAgentRateLimiter()
	// 8 sends within the window exhausts the per-sender budget even though
	// every recipient is a fresh pair (no per-pair cooldown ever trips).
	for i := 0; i < defaultSenderDMMax; i++ {
		r.recordDM("seat-a", string(rune('A'+i)))
	}
	b := r.checkDMBlock("seat-a", "peer-new", time.Second)
	if b.dimension != "sender-budget" {
		t.Fatalf("expected sender-budget dimension, got %q (remaining=%v)", b.dimension, b.remaining)
	}
	if !strings.Contains(b.message(), "dimension=sender-budget") {
		t.Fatalf("message must name the tripped dimension: %q", b.message())
	}
	if b.remaining <= 0 || b.remaining > defaultSenderDMWindow {
		t.Fatalf("sender-budget remaining out of window: %v", b.remaining)
	}
	// a different sender is unaffected: per-sender, not global
	if b2 := r.checkDMBlock("seat-b", "peer-new", time.Second); b2.dimension != "" {
		t.Fatalf("seat-b must be unblocked, got %q", b2.dimension)
	}
}

func TestIssue5558_PairCooldownDimension(t *testing.T) {
	r := newAgentRateLimiter()
	r.recordDM("seat-a", "peer-x")
	b := r.checkDMBlock("seat-a", "peer-x", time.Hour)
	if b.dimension != "pair" {
		t.Fatalf("expected pair dimension, got %q", b.dimension)
	}
	if strings.Contains(b.message(), "sender-budget") {
		t.Fatalf("pair block message must not mention sender budget: %q", b.message())
	}
}

// #5558 Residual 1: bounded opt-in retry queue - one pending per pair, overall
// cap, TTL expiry surfaced as explicit drop, replacement allowed.

func TestIssue5558_QueueOfferReplaceAndCap(t *testing.T) {
	q := newDMRetryQueue()
	now := time.Now()
	// fill one per pair
	for i := 0; i < dmQueueOverallCap; i++ {
		res := q.offer("s", string(rune('a'+i)), "m", true, now.Add(time.Second))
		if !res.Queued || res.Overflow {
			t.Fatalf("offer %d should queue, got %+v", i, res)
		}
	}
	// replacement on an existing pair is allowed even at cap
	res := q.offer("s", "a", "m2", true, now.Add(time.Second))
	if !res.Queued || !res.Replaced || res.Overflow {
		t.Fatalf("replacement expected: %+v", res)
	}
	// new pair at cap overflows - fail-safe rejection, never silent drop
	res = q.offer("s", "zzz", "m", true, now.Add(time.Second))
	if res.Queued || !res.Overflow {
		t.Fatalf("overflow expected: %+v", res)
	}
	if q.len() != dmQueueOverallCap {
		t.Fatalf("queue len = %d, want %d", q.len(), dmQueueOverallCap)
	}
}

func TestIssue5558_QueueTTLExpirySurfacesDrop(t *testing.T) {
	q := newDMRetryQueue()
	now := time.Now()
	q.offer("s", "r", "urgent", true, now.Add(50*time.Hour)) // cooldown far away
	// TTL expires before the cooldown is reachable
	key, _, ok, expired := q.takeDue("s", now.Add(dmQueueTTL+time.Minute))
	if key == "" || ok || !expired {
		t.Fatalf("expected TTL expiry (expired=true, ok=false), got key=%q ok=%v expired=%v", key, ok, expired)
	}
	if q.len() != 0 {
		t.Fatalf("expired entry must be removed, len=%d", q.len())
	}
}

func TestIssue5558_QueueTakeDueRespectsCooldown(t *testing.T) {
	q := newDMRetryQueue()
	now := time.Now()
	q.offer("s", "r", "m", true, now.Add(30*time.Second))
	// before cooldown end: nothing due, not expired
	if _, _, ok, expired := q.takeDue("s", now.Add(time.Second)); ok || expired {
		t.Fatal("entry must not be due or expired before cooldown end")
	}
	// after cooldown end: due
	key, e, ok, expired := q.takeDue("s", now.Add(31*time.Second))
	if key != "s\u2192r" || !ok || expired || e.text != "m" {
		t.Fatalf("expected due entry, got key=%q ok=%v expired=%v", key, ok, expired)
	}
	// other sender's entries untouched
	q.offer("other", "r", "x", true, now.Add(time.Second))
	if _, _, ok, _ = q.takeDue("s", now.Add(time.Minute)); ok {
		t.Fatal("no more entries for sender s")
	}
}

// Disabled-by-default: without LANCHAT_DM_RETRY_QUEUE=1 the tool has no queue
// and blocked sends keep the #4785 behaviour (error with sleep hint).
func TestIssue5558_QueueDisabledByDefault(t *testing.T) {
	if dmQueueEnabled() {
		t.Skip("LANCHAT_DM_RETRY_QUEUE=1 set in env; default-off assertion skipped")
	}
	lt := &LanChatTool{}
	queued, line := lt.tryQueueBlockedDM("s", "r", "m", true, dmBlock{dimension: "pair", remaining: time.Minute})
	if queued || line != "" {
		t.Fatalf("disabled queue must not enqueue: queued=%v line=%q", queued, line)
	}
}
