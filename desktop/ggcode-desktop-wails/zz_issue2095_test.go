package main

// #2095 bug A regression: showOSNotification must report queue rejection
// (bool) so the Notify/NotifyApprovalNeeded rollback contract covers the
// Linux branch (which shares darwin's unixQueue) and non-Windows approvals,
// not just the darwin and Windows branches. The platform branch itself is
// runtime.GOOS-hardcoded and cannot be covered on a single host; this test
// pins the darwin/linux delivery-acceptance contract that the rollback
// callers depend on.

import (
	"runtime"
	"testing"
	"time"
)

func TestShowOSNotificationReportsQueueFull(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix queue path")
	}
	// Construct without workers: NewNotificationManager starts a drainUnix
	// goroutine that would empty the queue behind our backs.
	nm := &NotificationManager{
		lastShown: make(map[string]time.Time),
		unixQueue: make(chan unixToast, 32),
	}
	// Fill the bounded unixQueue (capacity 32) with no worker draining it.
	for i := 0; i < cap(nm.unixQueue); i++ {
		if !nm.enqueueUnixToast("fill", "x") {
			t.Fatalf("queue filled prematurely at %d", i)
		}
	}
	if nm.showOSNotification("title", "body") {
		t.Fatal("showOSNotification must report rejection when the unix queue is full (rollback contract #2095)")
	}
}
