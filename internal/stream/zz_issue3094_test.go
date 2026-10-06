package stream

// Regression probe for #3094: the broadcaster's internal exit paths must
// Stop their own targets (write-failure and encoder-nil exits previously
// left the ffmpeg pusher child and the upstream RTMP connection dangling
// until the outer Manager.Stop()).
//
// Unit-level anchor on the idempotence contract the fix relies on:
// stopping an already-stopped/never-started target is a safe no-op, and
// Stop() transitions a target out of its running/error state, so calling
// it from broadcaster goroutines can never double-kill.

import (
	"testing"
	"time"
)

// TestIssue3094_StopIsIdempotent pins the safety contract that makes the
// broadcaster-side Stop calls zero-risk.
func TestIssue3094_StopIsIdempotent(t *testing.T) {
	tgt := NewTarget("probe", "rtmp://127.0.0.1:1935/live/probe")
	// Never started: Stop must be a no-op, not a panic or state churn.
	tgt.Stop()
	tgt.Stop()
	st := tgt.Status()
	if st.State == TargetLive {
		t.Fatalf("never-started target reports running after Stop: %+v", st)
	}
}

// TestIssue3094_StopNeverBlocks: repeated and concurrent Stops (the
// broadcaster path races Manager.Stop and target_monitor) must return
// promptly - lock-guarded idempotence.
func TestIssue3094_StopNeverBlocks(t *testing.T) {
	tgt := NewTarget("probe2", "rtmp://127.0.0.1:1/live/x")
	tgt.Stop()
	tgt.Stop()
	done := make(chan struct{})
	go func() { tgt.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on an already-stopped target")
	}
}
