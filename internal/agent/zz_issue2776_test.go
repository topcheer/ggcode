package agent

// #2776: channel_safety detectors must not flag Go's most idiomatic safe
// patterns — close+return producer exit, close+break loop exit, and mutex
// if/else branches. Previously all three detectors were purely
// source-order-linear with zero control-flow awareness, producing confident
// "will panic" false positives (3 at once for the standard producer loop).

import (
	"strings"
	"testing"
)

// TestIssue2776ProducerCloseReturnNoFalsePositive pins scenario 1: a producer
// loop with close+return early exit plus a trailing close after the loop.
// Pre-fix: close-in-loop + double-close + send-after-close (3 false alarms).
func TestIssue2776ProducerCloseReturnNoFalsePositive(t *testing.T) {
	src := `package p

func produce(items []int) {
	ch := make(chan int)
	go func() {
		for _, v := range items {
			if v < 0 {
				close(ch)
				return
			}
			ch <- v
		}
		close(ch)
	}()
	_ = ch
}
`
	warnings := checkChannelSafety("p.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for safe producer close+return, got %d: %v", len(warnings), warnings)
	}
}

// TestIssue2776MutexBranchCloseNoFalsePositive pins scenario 2: error-path
// close+return in a mutex branch, then normal-path send+close.
// Pre-fix: double-close + send-after-close (2 false alarms).
func TestIssue2776MutexBranchCloseNoFalsePositive(t *testing.T) {
	src := `package p

func send(ch chan int, err error) error {
	if err != nil {
		close(ch)
		return err
	}
	ch <- 1
	close(ch)
	return nil
}
`
	warnings := checkChannelSafety("p.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for mutex-branch close idiom, got %d: %v", len(warnings), warnings)
	}
}

// TestIssue2776CloseBreakLoopNoFalsePositive: close+break exits the loop
// before a second iteration can close again.
func TestIssue2776CloseBreakLoopNoFalsePositive(t *testing.T) {
	src := `package p

func drain(list []int) {
	ch := make(chan int, 1)
	for _, v := range list {
		if v < 0 {
			close(ch)
			break
		}
		ch <- v
	}
}
`
	warnings := checkChannelSafety("p.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for close+break loop exit, got %d: %v", len(warnings), warnings)
	}
}

// TestIssue2776MutexSiblingClosesNoFalsePositive: closes on different sides
// of the same if/else never both run — with return terminators.
func TestIssue2776MutexSiblingClosesNoFalsePositive(t *testing.T) {
	src := `package p

func pick(ch chan int, failed bool) {
	if failed {
		close(ch)
		return
	} else {
		close(ch)
		return
	}
}
`
	warnings := checkChannelSafety("p.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for mutex sibling closes, got %d: %v", len(warnings), warnings)
	}
}

// TestIssue2776TruePositivesStillDetected guards against over-suppression:
// the genuinely dangerous patterns must still be flagged.
func TestIssue2776TruePositivesStillDetected(t *testing.T) {
	t.Run("double close sequential", func(t *testing.T) {
		src := `package p
func f() {
	ch := make(chan int)
	close(ch)
	close(ch)
}
`
		w := checkChannelSafety("p.go", "", src)
		if len(w) != 1 || !strings.Contains(w[0], "Double channel close") {
			t.Fatalf("expected double-close detection, got %v", w)
		}
	})
	t.Run("send after close", func(t *testing.T) {
		src := `package p
func f() {
	ch := make(chan int)
	close(ch)
	ch <- 1
}
`
		w := checkChannelSafety("p.go", "", src)
		if len(w) != 1 || !strings.Contains(w[0], "Send after close") {
			t.Fatalf("expected send-after-close detection, got %v", w)
		}
	})
	t.Run("close in loop without terminator", func(t *testing.T) {
		src := `package p
func f(items []int) {
	ch := make(chan int)
	for _, v := range items {
		ch <- v
		close(ch)
	}
}
`
		w := checkChannelSafety("p.go", "", src)
		if len(w) != 1 || !strings.Contains(w[0], "close inside loop") {
			t.Fatalf("expected close-in-loop detection, got %v", w)
		}
	})
}
