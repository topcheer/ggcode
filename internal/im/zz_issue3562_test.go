package im

// #3562 probe: IRC reconnect backoff must reset after a HEALTHY session
// (same family as matrix #432 / discord #389). The old loop only ever
// doubled the backoff - one early failure chain permanently pinned every
// later reconnect at the 120s cap.

import "testing"
import "time"

func TestIssue3562_HealthySessionResetsBackoff(t *testing.T) {
	got := ircBackoffAfterDisconnect(120*time.Second, 2*time.Hour)
	if got != ircReconnectBackoff {
		t.Fatalf("healthy session must reset to base %v, got %v", ircReconnectBackoff, got)
	}
	// Exactly at the threshold counts as healthy.
	if got := ircBackoffAfterDisconnect(120*time.Second, 60*time.Second); got != ircReconnectBackoff {
		t.Fatalf("threshold-long session must reset, got %v", got)
	}
}

func TestIssue3562_ShortSessionKeepsBackoff(t *testing.T) {
	if got := ircBackoffAfterDisconnect(40*time.Second, 5*time.Second); got != 40*time.Second {
		t.Fatalf("short session must keep accumulated backoff, got %v", got)
	}
	if got := ircBackoffAfterDisconnect(120*time.Second, 59*time.Second); got != 120*time.Second {
		t.Fatalf("just-under-threshold must keep cap, got %v", got)
	}
}
