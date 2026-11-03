package stream

import (
	"testing"
	"time"
)

func TestIssue3154_ReadErrorExitStopsTargets(t *testing.T) {

	fakeFFmpegDir(t, "sleep 0.05\nexit 1\n")
	enc := NewEncoder(4, 4, 26, 1, "")
	if err := enc.Start(); err != nil {
		t.Skipf("fake ffmpeg start failed: %v", err)
	}
	t.Cleanup(func() { _ = enc.Stop() })

	m := NewManager(StreamConfig{})
	tgt := NewTarget("probe3154", "rtmp://127.0.0.1:1/live/probe")

	_, _ = tgt.Connect()

	deadline := time.Now().Add(3 * time.Second)
	for {
		st := tgt.Status().State
		if st == TargetError {
			break
		}
		if time.Now().After(deadline) {
			t.Skipf("fake pusher did not surface an error state (state=%v); environment cannot exercise the probe", st)
		}
		time.Sleep(10 * time.Millisecond)
	}

	m.mu.Lock()
	m.targets[tgt.Name()] = tgt
	m.mu.Unlock()
	m.encoderMu.Lock()
	m.encoder = enc
	m.encoderMu.Unlock()

	done := make(chan struct{})
	go func() {
		m.fanOutBroadcaster()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broadcaster did not exit after the encoder read error")
	}

	if st := tgt.Status().State; st != TargetStopped {
		t.Fatalf("read-error exit must Stop its own targets (#3094 contract, #3154); target state = %v", st)
	}
}
