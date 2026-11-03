//go:build goolm

package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"testing"
)

func TestIssue3748_AbortVsTransportWritersNoRace(t *testing.T) {
	c := NewClient("race-3748", "/bin/cat", nil)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Abort()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			c.setTransportProcess(nil, nil, nil)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			c.setTransportProcess(exec.Command("true"), func() {}, nil)
			_ = c.snapshotTransport()
		}
	}()
	wg.Wait()

}

func TestIssue3747_LateResponseAtDeadlineIsReturned(t *testing.T) {
	c := NewClient("waiter-3747", "/bin/cat", nil)
	reqID := NewIntID(42)
	idVal := reqID
	waiter := make(chan *Response, 1)
	c.registerWaiter(&idVal, waiter)
	defer c.unregisterWaiter(&idVal, waiter)

	want := json.RawMessage("42")
	waiter <- &Response{ID: want, JSONRPC: "2.0"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := c.readResponseWithWaiter(ctx, &idVal, waiter)
	if err != nil {
		t.Fatalf("arrived response must win over an expired ctx, got err: %v", err)
	}
	if got == nil || string(got.ID) != string(want) {
		t.Fatalf("arrived response must be returned, got %+v", got)
	}
}
