package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStdioServer struct {
	pr *io.PipeReader
	pw *io.PipeWriter
	cr *io.PipeReader
	cw *io.PipeWriter
}

func newFakeStdioServer() *fakeStdioServer {
	pr, pw := io.Pipe()
	cr, cw := io.Pipe()
	return &fakeStdioServer{pr: pr, pw: pw, cr: cr, cw: cw}
}

func TestIssue644_TimeoutKeepsConnectionWhenOthersInFlight(t *testing.T) {
	srv := newFakeStdioServer()
	defer func() { _ = srv.pr.Close(); _ = srv.pw.Close(); _ = srv.cr.Close(); _ = srv.cw.Close() }()

	c := &Client{
		name:             "issue644-stdio",
		transport:        "stdio",
		stdin:            srv.cw,
		reader:           bufio.NewReader(srv.pr),
		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
	}

	go func() {
		scanner := bufio.NewScanner(srv.cr)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var req Request
			if err := json.Unmarshal([]byte(line), &req); err != nil || req.ID == nil {
				continue
			}

			id := *req.ID
			idJSON, _ := json.Marshal(&id)
			go func() {
				time.Sleep(800 * time.Millisecond)
				resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`+"\n", string(idJSON))
				_, _ = srv.pw.Write([]byte(resp))
			}()
		}
	}()

	ctxA, cancelA := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelA()

	var wg sync.WaitGroup
	errA := make(chan error, 1)
	errB := make(chan error, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()

		_ = c.sendRequest(ctxA, "tools/call", map[string]interface{}{"name": "slow"}, nil)
		errA <- ctxA.Err()
	}()

	time.Sleep(100 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		ctxB, cancelB := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelB()
		errB <- c.sendRequest(ctxB, "tools/call", map[string]interface{}{"name": "healthy"}, nil)
	}()

	select {
	case e := <-errA:
		if e == nil {
			t.Fatal("request A should have failed with its own ctx timeout")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request A did not return")
	}

	select {
	case e := <-errB:
		if e != nil {
			t.Fatalf("healthy request B must survive A's timeout, got: %v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("request B did not return — connection likely aborted by A's timeout")
	}

	if c.IsClosed() {
		t.Fatal("client must not be closed just because one request timed out while others were in flight")
	}
	wg.Wait()
}

func TestIssue644_LastWaiterStillAborts(t *testing.T) {
	srv := newFakeStdioServer()
	defer func() { _ = srv.pr.Close(); _ = srv.pw.Close(); _ = srv.cr.Close(); _ = srv.cw.Close() }()

	c := &Client{
		name:             "issue644-solo",
		transport:        "stdio",
		stdin:            srv.cw,
		reader:           bufio.NewReader(srv.pr),
		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := srv.cr.Read(buf); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := c.sendRequest(ctx, "tools/call", map[string]interface{}{"name": "x"}, nil)
	if err == nil {
		t.Fatal("expected ctx timeout error")
	}
	if !c.IsClosed() {
		t.Fatal("solo timed-out request must still Abort (last waiter) — connection torn down")
	}
}

func TestIssue644_HasOtherWaiters(t *testing.T) {
	c := &Client{name: "gate"}
	ch1 := make(chan *Response, 1)
	ch2 := make(chan *Response, 1)
	if c.hasOtherWaiters(ch1) {
		t.Fatal("no waiters registered at all")
	}
	id1 := NewIntID(1)
	c.registerWaiter(&id1, ch1)
	if c.hasOtherWaiters(ch1) {
		t.Fatal("self must not count as other")
	}
	if !c.hasOtherWaiters(ch2) {
		t.Fatal("a different caller must count as other")
	}
	if !c.hasOtherWaiters(nil) {
		t.Fatal("nil self: any waiter counts as other")
	}
	c.unregisterWaiter(&id1, ch1)
	if c.hasOtherWaiters(nil) {
		t.Fatal("after unregister, no waiters remain")
	}
}
