package mcp

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNotificationWorkerConcurrentAbortNoRace(t *testing.T) {
	c := NewClient("race-test", "/bin/cat", nil)
	var wg sync.WaitGroup
	ready := make(chan struct{})

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

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Abort()
	}()

	for i := 0; i < 200; i++ {
		c.processNotification(&Notification{Method: "test/notify", Params: json.RawMessage(`{}`)})
	}

	wg.Wait()
}

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
