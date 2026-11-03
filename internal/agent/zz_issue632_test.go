package agent

import (
	"strings"
	"sync"
	"testing"
)

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
			<-start
			checkRetryQuality("f.go", oldTimerHeavy, timerHeavy)
			checkRetryQuality("g.go", "", timerHeavy)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}
}

func TestIssue632_TimerIdentSemanticsPreserved(t *testing.T) {

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
