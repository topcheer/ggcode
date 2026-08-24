package mcp

// Issue #994 companion tests.
//
// Problem 1 (high): the stdio branch of sendRequestUnlocked wrote the
// request under c.mu and only registered the response waiter afterwards
// (inside readResponseWithCancel), leaving an unlocked gap between write
// completion and registration. A concurrent caller's read loop could
// consume our response in that gap, deliverResponse would find no waiter
// ("dropping response with unknown ID"), and the orphaned request would
// idle-wait the full mcpRequestTimeout — and as the sole waiter, Abort()
// the shared connection other callers were still using. The WS path had
// been fixed for this in #523/#156 (register-before-write); the stdio path
// is now aligned (waiter registered before writeMessageUnlocked, unregistered
// via caller-side defer, the ctx.Done early-unregister, and the read
// goroutine's defer — unregisterWaiter is channel-matched and idempotent).
//
// Problem 2 (low/latent): sendWSUnlocked and respondToServerRequestWS
// dereferenced c.wsConn under c.mu without a nil check, although
// sendWSNotification proves the nil state is reachable — a nil deref
// panic in a goroutine with no safego recovery.
//
// Test-level tradeoff for problem 1: the register-before-write ordering
// cannot be asserted deterministically through the package's own seams,
// because every observation point of the waiter table is c.mu-guarded and
// the requester holds c.mu across the entire write (a hook inside
// stdin.Write blocks on c.mu; sampling after the write completes races
// with registration in the OLD code too). So the ordering is covered
// behaviorally — a fast read-and-respond fake server plus many concurrent
// sendRequest calls, where the pre-fix window deterministically-in-probability
// produces dropped responses and timeouts — plus a deterministic
// write-failure no-waiter-leak assertion pinning the cleanup path.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// stdioFakeServer wires a Client's stdin to a fake "server" goroutine that
// reads one NDJSON request per line and IMMEDIATELY writes the response
// with the same ID ("read-and-respond"). Responding as fast as possible
// maximizes the probability of hitting the pre-#994 registration window:
// the response lands on the pipe before the requesting goroutine could have
// finished registering its waiter under the old ordering.
func stdioFakeServer(t *testing.T) (*Client, func()) {
	t.Helper()

	reqRead, reqWrite, err := os.Pipe() // client writes requests here
	if err != nil {
		t.Fatalf("request pipe: %v", err)
	}
	respRead, respWrite, err := os.Pipe() // server writes responses here
	if err != nil {
		t.Fatalf("response pipe: %v", err)
	}

	client := &Client{
		name:      "stdio-fake",
		transport: "stdio",
		stdin:     reqWrite,
		reader:    bufio.NewReader(respRead),
	}

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		scanner := bufio.NewScanner(reqRead)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var req struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				continue // not a request we can answer
			}
			resp, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]interface{}{"echo": req.ID},
			})
			respWrite.Write(append(resp, '\n'))
		}
	}()

	cleanup := func() {
		reqWrite.Close()
		reqRead.Close()
		respWrite.Close()
		respRead.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			t.Error("fake stdio server did not exit")
		}
	}
	return client, cleanup
}

// TestIssue994StdioConcurrentFastResponses is the behavioral assertion for
// problem 1: many concurrent sendRequest calls against a server that
// responds the instant it reads a request must all complete promptly with
// correctly routed results — no request may hit its deadline because the
// response was dropped by another caller's read loop ("dropping response
// with unknown ID"). Under the old write-then-register ordering, a request
// whose response was consumed inside the window idled for the full
// per-request timeout (mcpRequestTimeout, bounded by the test ctx) and, as
// the sole waiter, tore down the shared connection via Abort().
func TestIssue994StdioConcurrentFastResponses(t *testing.T) {
	client, cleanup := stdioFakeServer(t)
	defer cleanup()

	const n = 24
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
			defer rcancel()
			var out struct {
				Echo int64 `json:"echo"`
			}
			if err := client.sendRequest(rctx, "test/concurrent", map[string]interface{}{}, &out); err != nil {
				errs <- fmt.Errorf("req %d: %w", i, err)
				return
			}
			if out.Echo == 0 {
				errs <- fmt.Errorf("req %d: response misrouted (echo=0)", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// errStdin always fails, exercising the write-failure path of the fixed
// stdio branch: the waiter registered before the write must be
// unregistered before the error returns (no ghost waiter leak).
type errStdin struct{}

func (errStdin) Write(p []byte) (int, error) { return 0, errors.New("synthetic stdin failure") }
func (errStdin) Close() error                { return nil }

// TestIssue994StdioWriteFailureNoWaiterLeak pins the cleanup half of the
// problem-1 fix deterministically: when the write fails, sendRequest must
// return an error AND leave the waiter table empty (the caller-side defer
// unregister runs on the error path too; a leaked ghost waiter would keep
// hasOtherWaiters true forever and permanently block the only Abort path,
// cf. #652).
func TestIssue994StdioWriteFailureNoWaiterLeak(t *testing.T) {
	client := &Client{
		name:      "stdio-write-fail",
		transport: "stdio",
		stdin:     errStdin{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := NewIntID(1)
	req := Request{JSONRPC: "2.0", Method: "test/method", Params: json.RawMessage(`{}`), ID: &id}
	if _, err := client.sendRequestUnlocked(req, ctx); err == nil {
		t.Fatal("expected write error from failing stdin")
	}

	client.mu.Lock()
	leaked := len(client.waiters)
	client.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("waiter leaked after write failure: %d entries remain", leaked)
	}
}

// TestIssue994SendWSUnlockedNilConnNoPanic pins problem 2 for
// sendWSUnlocked: with no established WS connection the call must return an
// error, not panic on the nil wsConn dereference. The request carries an ID
// so the fixed path also exercises the register/unregister waiter
// bookkeeping around the guarded write.
func TestIssue994SendWSUnlockedNilConnNoPanic(t *testing.T) {
	c := NewClient("ws-nil", "ws", nil)
	if c.wsConn != nil {
		t.Fatal("precondition: fresh ws client should have nil wsConn")
	}
	id := NewIntID(1)
	req := Request{JSONRPC: "2.0", Method: "test/method", Params: json.RawMessage(`{}`), ID: &id}
	resp, err := c.sendWSUnlocked(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error for nil wsConn, got resp=%v", resp)
	}
	if c.wsConn != nil {
		t.Fatal("wsConn must remain nil")
	}
	// Waiter bookkeeping must not leak after the write failure.
	c.mu.Lock()
	leaked := len(c.waiters)
	c.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("waiter leaked after failed ws write: %d entries remain", leaked)
	}
}

// TestIssue994RespondToServerRequestWSNilConnNoPanic pins problem 2 for
// respondToServerRequestWS: same nil-guard alignment with
// sendWSNotification, whose explicit c.wsConn == nil check documents that
// the state is reachable.
func TestIssue994RespondToServerRequestWSNilConnNoPanic(t *testing.T) {
	c := NewClient("ws-nil", "ws", nil)
	id := NewIntID(7)
	req := &Request{JSONRPC: "2.0", Method: "sampling/createMessage", ID: &id}
	if err := c.respondToServerRequestWS(req); err == nil {
		t.Fatal("expected error for nil wsConn, not nil")
	}
}
