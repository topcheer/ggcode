//go:build goolm

package agentruntime

// Regression test for GitHub issue #4565: TunnelHost.Close stopped the
// online/ref brokers (#4503) but only nil'd the offline projection broker,
// leaking its senderLoop + textFlushLoop goroutines and 300ms ticker on
// every runtime teardown (desktop session switches leak cumulatively).

import (
	"runtime"
	"testing"
	"time"
)

// After Close, every goroutine NewBroker(nil) started for the projection
// broker must be gone: the count returns to the pre-host baseline.
func TestIssue4565_CloseStopsProjectionBroker(t *testing.T) {
	// Warm up any lazy goroutines so the baseline is stable.
	h0 := NewTunnelHost()
	h0.Close()
	time.Sleep(150 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	h := NewTunnelHost() // creates projBroker = tunnel.NewBroker(nil)
	h.Close()
	// Give the two stopped loops time to observe textDone/outDone and exit.
	time.Sleep(150 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > baseline {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutines leaked after Close: before=%d after=%d\n%s", baseline, after, buf[:n])
	}
}

// Control for the measurement above: an UNSTOPPED broker keeps its loops
// alive, proving the convergence in the previous test is meaningful.
func TestIssue4565_UnstoppedBrokerKeepsLoops(t *testing.T) {
	h0 := NewTunnelHost()
	h0.Close()
	time.Sleep(150 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	h := NewTunnelHost()
	defer h.Close() // do not double-leak in the test binary
	time.Sleep(150 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after < baseline+1 {
		t.Fatalf("expected the live projection broker to hold its goroutines (before=%d after=%d); measurement is not sensitive", baseline, after)
	}
}

// Close must stay idempotent with a projection broker present.
func TestIssue4565_CloseIdempotent(t *testing.T) {
	h := NewTunnelHost()
	h.Close()
	h.Close() // must not panic (Stop is sync.Once; fields are nil'd under lock)
}
