package mcp

// Regression tests for issue #644: a single request's ctx timeout must not
// unconditionally Abort the shared stdio/WS connection while other requests
// are in flight. The old code called c.Abort() in the ctx.Done branch of
// readResponseWithCancel (stdio) and readWSResponse (WS), killing concurrent
// healthy requests and permanently closing the client. The fix gates Abort on
// hasOtherWaiters: only the last (or only) waiter tears down the connection.

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

// fakeStdioServer is an in-memory MCP stdio server: requests are answered by
// the respond func, with per-request optional delay.
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

// TestIssue644_TimeoutKeepsConnectionWhenOthersInFlight: two concurrent
// stdio requests; req A has a 300ms ctx (times out), req B is answered by
// the server after 1s. Before the fix, A's timeout Aborted the connection,
// so B failed with a transport error. After the fix, B still succeeds and
// the client is NOT closed.
func TestIssue644_TimeoutKeepsConnectionWhenOthersInFlight(t *testing.T) {
	srv := newFakeStdioServer()
	defer func() { _ = srv.pr.Close(); _ = srv.pw.Close(); _ = srv.cr.Close(); _ = srv.cw.Close() }()

	c := &Client{
		name:             "issue644-stdio",
		transport:        "stdio",
		stdin:            srv.cw, // client writes into server's read end
		reader:           bufio.NewReader(srv.pr),
		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
	}

	// Server goroutine: parse NDJSON requests; answer req id=2 (slow one that
	// will time out) never, and answer id=1 after 800ms. Actually: A (times
	// out) = id assigned first; we answer only the second request after 800ms.
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
			// Answer every request after 800ms — request A has a 300ms ctx so
			// it times out before its answer; request B has 10s and succeeds.
			id := *req.ID
			idJSON, _ := json.Marshal(&id)
			go func() {
				time.Sleep(800 * time.Millisecond)
				resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`+"\n", string(idJSON))
				_, _ = srv.pw.Write([]byte(resp))
			}()
		}
	}()

	// Request A: short deadline → ctx timeout while B is in flight.
	ctxA, cancelA := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelA()

	var wg sync.WaitGroup
	errA := make(chan error, 1)
	errB := make(chan error, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()
		// A must FAIL with its ctx error, but must NOT abort the connection.
		_ = c.sendRequest(ctxA, "tools/call", map[string]interface{}{"name": "slow"}, nil)
		errA <- ctxA.Err()
	}()

	// Give A a head start so it's registered as a waiter first.
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

// TestIssue644_LastWaiterStillAborts: when the timing-out request is the ONLY
// waiter, the old fast-fail Abort behavior must be preserved (a dead server
// must not pin readMu forever).
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
	// Server never answers, but it MUST keep draining the request pipe:
	// stdin is an io.Pipe (synchronous), so an unconsumed write blocks
	// forever — without this drain the test itself would hang on the
	// request write, not on the timeout semantics under test (#644).
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

// TestIssue644_HasOtherWaiters unit-checks the gate directly.
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
