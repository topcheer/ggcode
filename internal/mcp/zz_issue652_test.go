package mcp

import (
	"bufio"
	"context"
	"os"
	"testing"
	"time"
)

func newHangingStdioClient(t *testing.T, name string) (*Client, func()) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		pr.Close()
		pw.Close()
		t.Fatal(err)
	}
	c := &Client{
		name:      name,
		transport: "stdio",
		stdin:     devnull,
		reader:    bufio.NewReader(pr),
	}
	cleanup := func() {
		pw.Close()
		pr.Close()
		devnull.Close()
	}
	return c, cleanup
}

func TestIssue652GhostWaiterDoesNotBlockAbort(t *testing.T) {
	c, cleanup := newHangingStdioClient(t, "hang-issue652")
	defer cleanup()

	id1 := NewIntID(1)
	id2 := NewIntID(2)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel1()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel2()

	errCh := make(chan error, 2)
	go func() {
		_, e := c.sendRequestUnlocked(Request{JSONRPC: "2.0", ID: &id1, Method: "ping"}, ctx1)
		errCh <- e
	}()
	time.Sleep(40 * time.Millisecond)
	go func() {
		_, e := c.sendRequestUnlocked(Request{JSONRPC: "2.0", ID: &id2, Method: "ping"}, ctx2)
		errCh <- e
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-errCh:
		case <-time.After(10 * time.Second):
			t.Fatal("cancelled requests did not return — ghost waiter wedged the connection (#652)")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.waiters)
		c.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	remaining := len(c.waiters)
	c.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 registered waiters after both requests cancelled, got %d (ghost waiter)", remaining)
	}

	id3 := NewIntID(3)
	ctx3, cancel3 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel3()
	done := make(chan error, 1)
	go func() {
		_, e := c.sendRequestUnlocked(Request{JSONRPC: "2.0", ID: &id3, Method: "ping"}, ctx3)
		done <- e
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected ctx error from post-hang request")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("post-hang request wedged — recovery path blocked by ghost waiter (#652)")
	}
	if !c.closed.Load() {
		t.Fatal("expected the transport to have been aborted by the last cancelled waiter")
	}
}

func TestIssue652CancelUnregistersWaiter(t *testing.T) {
	c, cleanup := newHangingStdioClient(t, "hang2-issue652")
	defer cleanup()

	id := NewIntID(7)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.sendRequestUnlocked(Request{JSONRPC: "2.0", ID: &id, Method: "ping"}, ctx)
	if err == nil {
		t.Fatal("expected ctx error")
	}
	c.mu.Lock()
	n := len(c.waiters)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("waiter still registered immediately after cancellation (got %d) — unregistration must be synchronous (#652)", n)
	}
}
