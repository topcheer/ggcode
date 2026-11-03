package stream

import (
	"testing"
	"time"
)

func TestIssue3094_StopIsIdempotent(t *testing.T) {
	tgt := NewTarget("probe", "rtmp://127.0.0.1:1935/live/probe")

	tgt.Stop()
	tgt.Stop()
	st := tgt.Status()
	if st.State == TargetLive {
		t.Fatalf("never-started target reports running after Stop: %+v", st)
	}
}

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
