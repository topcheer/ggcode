package lsp

import (
	"io"
	"os"
	"testing"
	"time"
)

func TestIssue3746_WriteTimeoutOnStuckPipe(t *testing.T) {

	pr, pw := io.Pipe()
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

	if err := c.write(msg); err == nil {
		t.Fatal("subsequent write on the closed session must fail")
	}
}

func TestIssue3746_NormalWriteUnaffected(t *testing.T) {

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
