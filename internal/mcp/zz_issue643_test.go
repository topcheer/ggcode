package mcp

// Regression tests for issue #643:
//   Bug 1 — crash-reconnect path: procWatch sets closed=true on unexpected
//           process exit WITHOUT running Abort(), so a subsequent Close()
//           hits the early-return branch and never closes notificationDone.
//           The notification dispatch worker goroutine then blocks forever
//           on {<-ch, <-done} — a permanent leak per crash-reconnect cycle.
//           Fix: the early-return branch now runs Abort() (idempotent via
//           abortOnce) before returning.
//   Bug 2 — NDJSON read path (readMessage '{' branch) used ReadBytes('\n')
//           with no length bound. #182 only capped the Content-Length branch.
//           A crashed/malicious stdio server emitting '{' + unbounded stream
//           with no newline would grow the bufio buffer without limit (OOM).
//           Fix: readBoundedLine caps accumulation at maxNDJSONLineLength.

import (
	"bufio"
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestIssue643_CloseAfterCrashRunsAbort (Bug 1): simulate the exact
// procWatch sequence on a stdio client — process exits unexpectedly →
// closed.Store(true) + close(processExit), no Abort(). Then Close() must
// still run the abortOnce cleanup: notificationDone must be closed so the
// notification worker exits instead of leaking.
func TestIssue643_CloseAfterCrashRunsAbort(t *testing.T) {
	c := NewClient("crash-server", "true", nil)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Wait for the real process to exit — procWatch then sets closed and
	// closes processExit WITHOUT Abort (the bug path).
	select {
	case <-c.ProcessExit():
	case <-time.After(5 * time.Second):
		t.Fatal("processExit not closed; test server did not exit")
	}
	if !c.IsClosed() {
		t.Fatal("client should be marked closed after process exit")
	}

	// Before the fix, Close() returned nil immediately here (early exit),
	// leaving notificationDone open forever. Now it must run Abort's cleanup.
	done := func() <-chan struct{} {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.notificationDone
	}()
	if done == nil {
		t.Fatal("notificationDone should be initialized by the constructor")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case <-done:
		// Good — the notification worker can now exit (no goroutine leak).
	case <-time.After(2 * time.Second):
		t.Fatal("notificationDone not closed after Close() on a crashed client — notification goroutine leaks")
	}
}

// TestIssue643_CloseAfterCrashTwiceIdempotent: repeated Close() on the
// crashed client stays safe (abortOnce guarantees a single close of
// notificationDone; a second close would panic).
func TestIssue643_CloseAfterCrashTwiceIdempotent(t *testing.T) {
	c := NewClient("crash-server-2", "true", nil)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	select {
	case <-c.ProcessExit():
	case <-time.After(5 * time.Second):
		t.Fatal("processExit not closed")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Third call via Abort directly also must not panic.
	c.Abort()
}

// TestIssue643_NDJSONLineLengthBounded (Bug 2): a line longer than
// maxNDJSONLineLength must error instead of accumulating without bound.
// We use a small fake reader — we can't ship 16MB through ReadSlice on a
// real pipe cheaply, so we validate readBoundedLine directly plus the
// readMessage wiring with a normal line.
func TestIssue643_NDJSONLineLengthBounded(t *testing.T) {
	// Direct unit: exceed the cap → error, not unbounded growth.
	huge := strings.NewReader("{" + strings.Repeat("a", maxNDJSONLineLength+1024) + "}\n")
	r := bufio.NewReaderSize(huge, 4096)
	_, err := readBoundedLine(r, maxNDJSONLineLength)
	if err == nil {
		t.Fatal("expected error for line exceeding maxNDJSONLineLength")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Legit line passes through unchanged, including the newline.
	legit := bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
	line, err := readBoundedLine(legit, maxNDJSONLineLength)
	if err != nil {
		t.Fatalf("legit line failed: %v", err)
	}
	if !strings.HasSuffix(string(line), "\n") {
		t.Fatal("line should retain its trailing newline")
	}
}

// TestIssue643_ReadMessageUsesBoundedNDJSON: readMessage's '{' branch must
// parse a normal NDJSON message successfully through readBoundedLine.
func TestIssue643_ReadMessageUsesBoundedNDJSON(t *testing.T) {
	c := &Client{name: "ndjson"}
	c.reader = bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}` + "\n"))
	msg, err := c.readMessage(context.Background())
	if err != nil {
		t.Fatalf("readMessage failed: %v", err)
	}
	resp, ok := msg.(*Response)
	if !ok {
		t.Fatalf("expected *Response, got %T", msg)
	}
	if string(resp.ID) != "7" {
		t.Fatalf("unexpected id %s", resp.ID)
	}
}

// TestIssue643_NoNotificationGoroutineLeak: end-to-end goroutine accounting —
// after crash + Close, no goroutine still parked in the notification worker
// for the old client. We count mcp.client.notifications goroutines via
// runtime.Stack before/after.
func countNotificationGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	count := 0
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if strings.Contains(line, "mcp.client.notifications") {
			count++
		}
	}
	return count
}

func TestIssue643_NoNotificationGoroutineLeak(t *testing.T) {
	// Warm up any background goroutines from package init.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := countNotificationGoroutines()

	const cycles = 3
	for i := 0; i < cycles; i++ {
		c := NewClient("leak-server", "true", nil)
		if err := c.Start(context.Background()); err != nil {
			t.Fatalf("Start %d failed: %v", i, err)
		}
		// Queue a notification so the worker goroutine is actually spawned.
		c.processNotification(&Notification{JSONRPC: "2.0", Method: "notifications/test"})
		select {
		case <-c.ProcessExit():
		case <-time.After(5 * time.Second):
			t.Fatalf("cycle %d: processExit not closed", i)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("cycle %d Close: %v", i, err)
		}
	}
	// Give leaked workers (if any) time to be observed parked.
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	after := countNotificationGoroutines()
	if after > before {
		t.Fatalf("notification worker goroutines leaked: before=%d after=%d (each crash-reconnect must release its worker)", before, after)
	}
}

// Silence unused-import guard for io (used indirectly via reader types).
var _ io.Reader = (*strings.Reader)(nil)
