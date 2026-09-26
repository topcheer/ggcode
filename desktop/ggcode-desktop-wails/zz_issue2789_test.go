package main

import (
	"testing"
	"time"
)

// #2789: rollbackNotify must return the post-rollback count so Notify's
// tail frontend emit (#2410 title contract) no longer carries the stale
// pre-rollback bump, and must refresh/clear the badge inside the rollback
// (Notify already set it from the pre-rollback snapshot at commit time).
func TestIssue2789RollbackNotifyReturnsPostRollbackCount(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)

	// Simulate Notify's commit sequence: unread++, dedup-map entry.
	nm.mu.Lock()
	nm.unread = 3
	nm.lastShown["k"] = time.Now()
	pre := nm.unread
	nm.mu.Unlock()

	got := nm.rollbackNotify("k")
	if got != 2 {
		t.Errorf("rollbackNotify should return post-rollback count 2, got %d", got)
	}
	if nm.GetUnread() != 2 {
		t.Errorf("unread after rollback should be 2, got %d", nm.GetUnread())
	}
	if pre != 3 {
		t.Errorf("pre-rollback snapshot should have been 3, got %d", pre)
	}
	// The dedup-map entry must stay removed so a retry within the window
	// re-queues instead of hitting the dedup branch (#600 N4).
	nm.mu.Lock()
	_, stillThere := nm.lastShown["k"]
	nm.mu.Unlock()
	if stillThere {
		t.Error("dedup-map entry should be removed by rollbackNotify")
	}
}

// Zero-clamp on double rollback: count must never go negative and the
// returned value must reflect the clamp (0), not the raw decrement.
func TestIssue2789RollbackNotifyClampsAtZero(t *testing.T) {
	nm := NewNotificationManager()
	nm.mu.Lock()
	nm.unread = 0
	nm.mu.Unlock()

	if got := nm.rollbackNotify("missing"); got != 0 {
		t.Errorf("rollbackNotify at 0 should clamp to 0, got %d", got)
	}
	if nm.GetUnread() != 0 {
		t.Errorf("unread must never go negative, got %d", nm.GetUnread())
	}
}
