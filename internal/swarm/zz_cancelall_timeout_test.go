package swarm

import (
	"testing"
	"time"
)

func TestCancelAllPerTeammateTimeoutBudget(t *testing.T) {
	doneChs := make([]chan struct{}, 0, 3)
	stuck := make(chan struct{})
	readyBefore := make(chan struct{})
	readyAfter := make(chan struct{})
	close(readyBefore)
	close(readyAfter)

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

	if consumed != 2 {
		t.Fatalf("healthy channels must be consumed even around a stuck one, consumed=%d", consumed)
	}
}

func TestCancelAllHealthyChannelsNeverTimeOut(t *testing.T) {

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
