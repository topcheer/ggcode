package main

import (
	"runtime"
	"testing"
	"time"
)

func TestShowOSNotificationReportsQueueFull(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix queue path")
	}

	nm := &NotificationManager{
		lastShown: make(map[string]time.Time),
		unixQueue: make(chan unixToast, 32),
	}

	for i := 0; i < cap(nm.unixQueue); i++ {
		if !nm.enqueueUnixToast("fill", "x") {
			t.Fatalf("queue filled prematurely at %d", i)
		}
	}
	if nm.showOSNotification("title", "body") {
		t.Fatal("showOSNotification must report rejection when the unix queue is full (rollback contract #2095)")
	}
}
