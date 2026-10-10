package lsp

// #3746 probe: write() must bound the stdin write. A half-dead server (alive
// process, stopped reading stdin) fills the os.Pipe buffer; the old
// unbounded Write held writeMu forever, so ctx timeouts, close(), and even
// Process.Kill() (which runs behind the same write) could never fire. The
// bounded write returns an error, marks the client failed, and closes the
// pipe to unblock the stuck goroutine.

import (
	"io"
	"os"
	"testing"
	"time"
)

func TestIssue3746_WriteTimeoutOnStuckPipe(t *testing.T) {
	// os.Pipe with no reader drain: the pipe buffer is 64KB, so first fill
	// it synchronously, then the next Write blocks (a half-dead server).
	pr, pw := io.Pipe() // io.Pipe: EVERY write blocks until read
	defer pr.Close()

	c := &stdioClient{stdin: pw, resolved: ResolvedServer{Binary: "probe"}}
	msg := rpcEnvelope{JSONRPC: "2.0", Method: "probe"}

	start := time.Now()
	err := c.write(msg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("write to an undrained pipe must time out with an error")
	}
	if elapsed >= writeTimeout+5*time.Second {
		t.Fatalf("write must be bounded by ~%v, took %v", writeTimeout, elapsed)
	}
	if !c.isFailed() {
		t.Fatal("timed-out write must mark the client failed (fail-fast)")
	}
	// The session must be unusable: the stuck goroutine unblocked via close
	// (EPIPE) and subsequent writes fail fast instead of hanging.
	if err := c.write(msg); err == nil {
		t.Fatal("subsequent write on the closed session must fail")
	}
}

func TestIssue3746_NormalWriteUnaffected(t *testing.T) {
	// A drained pipe behaves exactly as before.
	pr, pw, _ := os.Pipe()
	defer pr.Close()
	go io.Copy(io.Discard, pr)

	c := &stdioClient{stdin: pw, resolved: ResolvedServer{Binary: "probe"}}
	if err := c.write(rpcEnvelope{JSONRPC: "2.0", Method: "probe"}); err != nil {
		t.Fatalf("normal write must succeed: %v", err)
	}
	if c.isFailed() {
		t.Fatal("a successful write must not mark the client failed")
	}
	pw.Close()
}
