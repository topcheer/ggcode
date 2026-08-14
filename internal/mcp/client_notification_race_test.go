package mcp

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestNotificationWorkerConcurrentAbortNoRace is the regression test for
// #292: the notification channel and done channel were previously created
// lazily inside the notificationOnce.Do closure, so a concurrent Abort()
// (which reads c.notificationDone and closes it without ever passing through
// the Once) had no happens-before edge with the writer. Under -race this
// reliably reported a data race. The fix initializes both fields in the
// constructors before the Client crosses goroutine boundaries; this test
// exercises the concurrent path so the race detector can prove it clean.
func TestNotificationWorkerConcurrentAbortNoRace(t *testing.T) {
	c := NewClient("race-test", "/bin/cat", nil)
	var wg sync.WaitGroup
	ready := make(chan struct{})

	// Goroutine A: register handler and dispatch notifications (enters
	// notificationOnce.Do, starts the worker).
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.SetNotificationHandler(func(method string, params json.RawMessage) {})
		close(ready)
		for i := 0; i < 200; i++ {
			c.processNotification(&Notification{Method: "test/notify", Params: json.RawMessage(`{}`)})
		}
	}()

	<-ready

	// Goroutine B: concurrent Abort() — previously raced on the lazy writes
	// of c.notificationCh / c.notificationDone inside the Once closure.
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Abort()
	}()

	// More dispatch attempts from the main goroutine while Abort runs.
	for i := 0; i < 200; i++ {
		c.processNotification(&Notification{Method: "test/notify", Params: json.RawMessage(`{}`)})
	}

	wg.Wait()
}

// TestNotificationChannelsInitializedAtConstruction ensures both channels are
// non-nil immediately after construction (#292 invariant: no lazy init).
func TestNotificationChannelsInitializedAtConstruction(t *testing.T) {
	c := NewClient("ctor-test", "/bin/cat", nil)
	if c.notificationCh == nil {
		t.Error("notificationCh must be initialized in NewClient")
	}
	if c.notificationDone == nil {
		t.Error("notificationDone must be initialized in NewClient")
	}

	c2 := NewClientFromConfig(config.MCPServerConfig{Name: "ctor-test-2", Type: "stdio", Command: "/bin/cat"})
	if c2 == nil {
		t.Fatal("NewClientFromConfig returned nil")
	}
	if c2.notificationCh == nil {
		t.Error("notificationCh must be initialized in NewClientFromConfig")
	}
	if c2.notificationDone == nil {
		t.Error("notificationDone must be initialized in NewClientFromConfig")
	}
}
