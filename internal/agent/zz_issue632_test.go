package agent

// #632: the #618 fix used a package-global `retryTimerIdents` map mutated
// without a lock. Two goroutines entering checkRetryQuality concurrently
// (parallel tool checks / multiple subagents in one process) hit a fatal
// concurrent-map-write panic that runChecksParallel's recover() cannot catch.
// The map is now per-call (collectTimerIdents returns a fresh map), so
// concurrent invocations share no state.

import (
	"strings"
	"sync"
	"testing"
)

// Concurrent checkRetryQuality calls over timer-heavy Go sources must not
// race (fatal concurrent map read/write under -race, hard crash without).
func TestIssue632_ParallelCheckRetryQualityNoRace(t *testing.T) {
	timerHeavy := `package x
import "time"
func f() error {
	for {
		err := do()
		if err != nil {
			t := time.NewTimer(time.Second)
			<-t.C
			continue
		}
		return nil
	}
}
`
	oldTimerHeavy := strings.Replace(timerHeavy, "time.Second", "2*time.Second", 1)

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // maximize contention
			checkRetryQuality("f.go", oldTimerHeavy, timerHeavy)
			checkRetryQuality("g.go", "", timerHeavy) // no-old-content path too
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}
}

// The per-call map must still deliver the #618 semantics: a `<-x.C` receive
// only counts as backoff when x is a time.NewTimer/NewTicker result, and the
// delta-aware subtraction must work across old/new content independently.
func TestIssue632_TimerIdentSemanticsPreserved(t *testing.T) {
	// New timer receive -> recognized as backoff, no warning. Bounded loop so
	// only the backoff classification is under test.
	good := `package x
import "time"
func f() error {
	for attempt := 0; attempt < 3; attempt++ {
		err := do()
		if err != nil {
			t := time.NewTimer(time.Second)
			<-t.C
			continue
		}
		return nil
	}
	return nil
}
`
	if w := checkRetryQuality("f.go", "", good); len(w) != 0 {
		t.Fatalf("timer receive must count as backoff (#618), got warnings: %v", w)
	}
	// Event channel with a .C field -> NOT a timer, warning expected.
	bad := `package x
type sub struct{ C chan int }
func f(s *sub) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := do()
		if err != nil {
			<-s.C
			continue
		}
		return nil
	}
	return nil
}
`
	if w := checkRetryQuality("f.go", "", bad); len(w) == 0 {
		t.Fatal("non-timer .C receive must still be flagged as missing backoff (#618)")
	}
}
