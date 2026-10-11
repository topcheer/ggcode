package mcp

// #3817 source-order pin: the subscription must be registered in c.subs
// BEFORE the subscriptions/listen request is written to the stdio server.
// The server is an independent process that can flush its ack the instant
// the write returns; the read loop correlates acks via c.subs, so
// register-after-write drops the ack as unknown traffic (unreplayable) and
// ackCh hangs to its 15s timeout. A full fake-stdio-server harness is out
// of proportion for a reordering fix; the probe pins the order (delete the
// early registration or move the write ahead of it and this fails).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3817_SubscriptionRegisteredBeforeWrite(t *testing.T) {
	raw, err := os.ReadFile("subscriptions.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	listen := strings.Index(src, "write subscriptions/listen")
	if listen < 0 {
		t.Fatal("listen block not found")
	}
	reg := strings.LastIndex(src[:listen], "c.addSubscription(subscriptionIDKey(reqID), sub)")
	write := strings.LastIndex(src[:listen], "c.writeMessageUnlocked(req)")
	if reg < 0 || write < 0 {
		t.Fatal("registration or write call not found before the listen error return")
	}
	if reg > write {
		t.Fatalf("subscription registration (offset %d) must precede the write (offset %d)", reg, write)
	}
	// Rollback on write failure must clean the registration up too.
	if !strings.Contains(src, "c.removeSubscription(subscriptionIDKey(reqID), sub)") {
		t.Fatal("write-failure rollback must remove the subscription registration")
	}
}
