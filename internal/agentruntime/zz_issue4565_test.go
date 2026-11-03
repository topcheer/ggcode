//go:build goolm

package agentruntime

import (
	"runtime"
	"testing"
	"time"
)

func TestIssue4565_CloseStopsProjectionBroker(t *testing.T) {

	h0 := NewTunnelHost()
	h0.Close()
	time.Sleep(150 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	h := NewTunnelHost()
	h.Close()

	time.Sleep(150 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > baseline {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutines leaked after Close: before=%d after=%d\n%s", baseline, after, buf[:n])
	}
}

func TestIssue4565_UnstoppedBrokerKeepsLoops(t *testing.T) {
	h0 := NewTunnelHost()
	h0.Close()
	time.Sleep(150 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	h := NewTunnelHost()
	defer h.Close()
	time.Sleep(150 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after < baseline+1 {
		t.Fatalf("expected the live projection broker to hold its goroutines (before=%d after=%d); measurement is not sensitive", baseline, after)
	}
}

func TestIssue4565_CloseIdempotent(t *testing.T) {
	h := NewTunnelHost()
	h.Close()
	h.Close()
}
