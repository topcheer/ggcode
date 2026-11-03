package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3869_DualURLRejectedNotSwallowed(t *testing.T) {
	_, err := parseOptionTransportInstallArgs([]string{"https://a.com/api", "https://b.com/api"}, "http")
	if err == nil {
		t.Fatal("dual-URL -t http install must error, not swallow a.com as the name")
	}
	if !strings.Contains(err.Error(), "single URL") {
		t.Fatalf("error must name the single-URL rule, got: %v", err)
	}

	if _, err := parseOptionTransportInstallArgs([]string{"https://a.com/api"}, "http"); err != nil {
		t.Fatalf("bare URL form must pass: %v", err)
	}
	if _, err := parseOptionTransportInstallArgs([]string{"my-http", "https://a.com/api"}, "http"); err != nil {
		t.Fatalf("name+URL form must pass: %v", err)
	}
}

func TestIssue3869_RaceLoserCancelledNotOrphaned(t *testing.T) {
	raw, err := os.ReadFile("subscriptions.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	finalLock := strings.Index(src, "c.subListenState.Store(subStateSupported)")
	if finalLock < 0 {
		t.Fatal("EnableModernSubscriptions tail not found")
	}
	tail := src[:finalLock]
	if !strings.Contains(tail, "if cur := c.modernSub; cur != nil") {
		t.Fatal("final critical section must re-read modernSub (CAS)")
	}
	if !strings.Contains(tail, "sub.Cancel(fmt.Errorf") {
		t.Fatal("race loser must be cancelled, not orphaned")
	}

	if strings.Count(tail, "c.modernSub = sub") != 1 {
		t.Fatal("exactly one modernSub assignment expected in the tail")
	}
}
