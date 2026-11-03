package mcp

import (
	"testing"
	"time"
)

func TestClient_ProcessExitChannel(t *testing.T) {

	c := NewClient("test-server", "echo", []string{})

	if ch := c.ProcessExit(); ch != nil {
		t.Fatal("expected nil processExit channel before Start")
	}
	if c.IsClosed() {
		t.Fatal("expected client to not be closed before Start")
	}
}

func TestClient_IsClosed(t *testing.T) {
	c := NewClient("test", "echo", nil)
	if c.IsClosed() {
		t.Fatal("new client should not be closed")
	}

	c.closed.Store(true)
	if !c.IsClosed() {
		t.Fatal("client should report closed after setting closed flag")
	}
}

func TestClient_ProcessExitOnRealProcess(t *testing.T) {
	c := NewClient("test-crash", "true", nil)
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	ch := c.ProcessExit()
	if ch == nil {
		t.Fatal("expected non-nil processExit channel after Start")
	}

	select {
	case <-ch:

	case <-time.After(5 * time.Second):
		t.Fatal("processExit channel was not closed within 5s")
	}

	if !c.IsClosed() {
		t.Fatal("client should be closed after process exit")
	}
}
