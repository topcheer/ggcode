package agent

import (
	"strings"
	"testing"
)

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

func TestIssue2776TruePositivesStillDetected(t *testing.T) {
	t.Run("send after loop with close+break still flagged", func(t *testing.T) {

		src := `package p
func f(list []int) {
	ch := make(chan int, 1)
	for _, v := range list {
		if v < 0 {
			close(ch)
			break
		}
		ch <- v
	}
	ch <- 99
}
`
		w := checkChannelSafety("p.go", "", src)
		if len(w) != 1 || !strings.Contains(w[0], "Send after close") {
			t.Fatalf("expected send-after-close outside loop, got %v", w)
		}
	})
	t.Run("close+continue next-iteration send still flagged", func(t *testing.T) {

		src := `package p
func f(list []int) {
	ch := make(chan int, 1)
	for _, v := range list {
		if v < 0 {
			close(ch)
			continue
		}
		ch <- v
	}
}
`
		w := checkChannelSafety("p.go", "", src)
		found := false
		for _, msg := range w {
			if strings.Contains(msg, "Send after close") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected send-after-close detection after close+continue, got %v", w)
		}
	})
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
