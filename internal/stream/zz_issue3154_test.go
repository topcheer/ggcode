package stream

// Regression probe for #3154: the broadcaster's read-error exit path
// (encoder crash / broken pipe / EOF) must Stop its own targets, per the
// #3094 contract ("every internal exit path must Stop its own targets").
// #3094 fixed the write-failure and encoder-nil exits; the read-error
// return was the third leak - ffmpeg pushers and RTMP connections dangled
// until the outer Manager.Stop().
//
// Observability: a target whose pusher ffmpeg died is in TargetError; the
// broadcaster's stopAllTargets() must transition it to TargetStopped. An
// idle target would make Stop a no-op (state unchanged), so the probe
// first drives the target into the error state via the shared fake ffmpeg
// script that exits 1.

import (
	"testing"
	"time"
)

func TestIssue3154_ReadErrorExitStopsTargets(t *testing.T) {
	// The fake ffmpeg serves both the ENCODER (start fine, then exit 1
	// WITHOUT reading stdin - a `cat` would block on the encoder's
	// never-closed stdin and the stdout pipe would never close, so the
	// broadcaster's Read would never unblock) and the TARGET pusher (same
	// script -> dies -> monitor flips the target to TargetError).
	fakeFFmpegDir(t, "sleep 0.05\nexit 1\n")
	enc := NewEncoder(4, 4, 26, 1, "")
	if err := enc.Start(); err != nil {
		t.Skipf("fake ffmpeg start failed: %v", err)
	}
	t.Cleanup(func() { _ = enc.Stop() })

	m := NewManager(StreamConfig{})
	tgt := NewTarget("probe3154", "rtmp://127.0.0.1:1/live/probe")
	// Connect spawns the (fake) pusher; ignore the result - either Connect
	// reports the early exit itself, or the target monitor does moments
	// later. Both land in TargetError.
	_, _ = tgt.Connect()

	// Precondition: wait for the dead pusher to surface as TargetError so
	// the broadcaster's Stop() is observable as a state transition (an
	// idle/stopped target makes Stop a no-op and the probe vacuous).
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
