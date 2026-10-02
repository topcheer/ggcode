package stream

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// #3094: targetWriter must stop its target on every exit path (write error
// AND channel close) instead of leaking the ffmpeg child until Manager.Stop.
// Stop is idempotent, so Manager.Stop() later is a no-op.

type issue3094ErrStdin struct{}

func (issue3094ErrStdin) Write(p []byte) (int, error) { return 0, errors.New("rtmp broken pipe") }
func (issue3094ErrStdin) Close() error                { return nil }

type issue3094OkStdin struct{}

func (issue3094OkStdin) Write(p []byte) (int, error) { return len(p), nil }
func (issue3094OkStdin) Close() error                { return nil }

func issue3094LiveTarget(name string, stdin io.WriteCloser) *Target {
	t := &Target{name: name, url: "rtmp://example.invalid/live"}
	t.mu.Lock()
	t.state = TargetLive
	t.stdin = stdin
	t.mu.Unlock()
	return t
}

// stateForTest reads state under the target's lock.
func (t *Target) stateForTest() TargetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Path 1: write failure exits targetWriter -> target must be stopped.
func TestIssue3094TargetWriterStopsTargetOnWriteError(t *testing.T) {
	m := NewManager(StreamConfig{})
	tgt := issue3094LiveTarget("t1", issue3094ErrStdin{})
	ch := make(chan []byte, 1)
	done := make(chan struct{})
	go func() { m.targetWriter(tgt, ch); close(done) }()
	ch <- []byte("flvframe")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("targetWriter did not exit after write error")
	}
	if s := tgt.stateForTest(); s != TargetStopped {
		t.Errorf("after write-error exit target state = %s, want stopped (was: leaked live)", s)
	}
}

// Path 2: broadcaster closes the channel -> range ends -> target must be stopped.
func TestIssue3094TargetWriterStopsTargetOnChannelClose(t *testing.T) {
	m := NewManager(StreamConfig{})
	tgt := issue3094LiveTarget("t2", issue3094OkStdin{})
	ch := make(chan []byte, 1)
	done := make(chan struct{})
	go func() { m.targetWriter(tgt, ch); close(done) }()
	ch <- []byte("f") // healthy write
	close(ch)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("targetWriter did not exit after channel close")
	}
	if s := tgt.stateForTest(); s != TargetStopped {
		t.Errorf("after channel-close exit target state = %s, want stopped (was: leaked live)", s)
	}
}

// Path 3: broadcaster exiting because encoder is nil must stop every target.
func TestIssue3094BroadcasterNilEncoderStopsTargets(t *testing.T) {
	m := NewManager(StreamConfig{})
	t1 := issue3094LiveTarget("a", issue3094OkStdin{})
	t2 := issue3094LiveTarget("b", issue3094OkStdin{})
	m.mu.Lock()
	m.targets["a"] = t1
	m.targets["b"] = t2
	m.mu.Unlock()

	done := make(chan struct{})
	go func() { m.fanOutBroadcaster(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fanOutBroadcaster did not exit with nil encoder")
	}
	for _, tgt := range []*Target{t1, t2} {
		if s := tgt.stateForTest(); s != TargetStopped {
			t.Errorf("target %s state after broadcaster nil-encoder exit = %s, want stopped", tgt.Name(), s)
		}
	}
}

// Stop idempotence contract the fix relies on (double Stop must not panic).
func TestIssue3094StopIsIdempotent(t *testing.T) {
	tgt := issue3094LiveTarget("t3", issue3094OkStdin{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tgt.Stop() }()
	}
	wg.Wait()
	if s := tgt.stateForTest(); s != TargetStopped {
		t.Errorf("state after concurrent Stops = %s, want stopped", s)
	}
}
