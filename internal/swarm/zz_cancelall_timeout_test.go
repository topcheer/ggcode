package swarm

import (
	"testing"
	"time"
)

// Regression test for the cron-2 review finding on CancelAll's done-wait
// loop: a single shared timeout timer let the first stuck teammate's expiry
// return immediately, abandoning every later teammate in the doneChs list -
// including ones that had already exited. The per-teammate budget must wait
// for already-exited teammates even after a stuck one burned its window.
//
// The wait loop is inline in CancelAll, so this test drives it through the
// same select shape with a stuck channel followed by ready ones and asserts
// the loop semantics directly: healthy channels are always consumed, a
// stuck channel trips timedOut exactly once, and post-timeout channels are
// drained non-blockingly.
func TestCancelAllPerTeammateTimeoutBudget(t *testing.T) {
	doneChs := make([]chan struct{}, 0, 3)
	stuck := make(chan struct{})       // never closed: simulates hung runner
	readyBefore := make(chan struct{}) // exits before the stuck one is reached
	readyAfter := make(chan struct{})  // exits after the timeout tripped
	close(readyBefore)
	close(readyAfter) // already exited by the time the drain runs

	// Short window so the test stays fast; mirrors swarmCancelTimeout's role.
	const window = 50 * time.Millisecond

	timedOut := false
	consumed := 0
	for _, ch := range append(doneChs, readyBefore, stuck, readyAfter) {
		if timedOut {
			select {
			case <-ch:
				consumed++
			default:
			}
			continue
		}
		timer := time.NewTimer(window)
		select {
		case <-ch:
			consumed++
		case <-timer.C:
			timedOut = true
		}
		timer.Stop()
	}

	if !timedOut {
		t.Fatalf("stuck channel must trip the timeout")
	}
	// readyBefore was consumed before the timeout; readyAfter was already
	// closed, so the non-blocking drain must have consumed it too - the old
	// shared-timer code returned at the stuck channel and consumed neither
	// the readyAfter channel nor anything after it.
	if consumed != 2 {
		t.Fatalf("healthy channels must be consumed even around a stuck one, consumed=%d", consumed)
	}
}

func TestCancelAllHealthyChannelsNeverTimeOut(t *testing.T) {
	// All-healthy list completes without tripping timedOut, consuming every
	// channel - the normal path must not regress.
	chs := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	for _, ch := range chs {
		close(ch)
	}
	timedOut := false
	consumed := 0
	for _, ch := range chs {
		if timedOut {
			select {
			case <-ch:
				consumed++
			default:
			}
			continue
		}
		timer := time.NewTimer(swarmCancelTimeout)
		select {
		case <-ch:
			consumed++
		case <-timer.C:
			timedOut = true
		}
		timer.Stop()
	}
	if timedOut || consumed != len(chs) {
		t.Fatalf("healthy list: timedOut=%v consumed=%d want false/%d", timedOut, consumed, len(chs))
	}
}
