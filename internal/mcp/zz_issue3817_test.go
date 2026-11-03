package mcp

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

	if !strings.Contains(src, "c.removeSubscription(subscriptionIDKey(reqID), sub)") {
		t.Fatal("write-failure rollback must remove the subscription registration")
	}
}
